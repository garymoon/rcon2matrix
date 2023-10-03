// Based on the Mautrix example client with e2ee stripped out (we won't need it, and it requires CGO)
// https://raw.githubusercontent.com/mautrix/go/master/example/main.go

// Also includes work from Slava's TheRegulars rcon client (see rcon.go)
// https://github.com/TheRegulars/website/blob/master/backend/pkg/rcon/rcon.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/antzucaro/qstr"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

/*********************************************************
TODO:
- Handle other media types (emoji etc)
- Handle unexpected sync failure at end of startMatrix()
*********************************************************/

var (
	xonServer  ServerConfig
	configFile string
	config     Config
	debug      bool
	mClient    *mautrix.Client
	mRoom      *mautrix.RespJoinRoom
)

func main() {
	flag.StringVar(&configFile, "config", "", "The path to the config file you wish to use")
	flag.BoolVar(&debug, "debug", false, "Enable debug logs")
	flag.Parse()

	config = getConfig()

	xonServer = ServerConfig{
		config.XonServer,
		config.XonPort,
		config.RconPassword,
		config.RconMode,
	}

	log := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()
	if !debug {
		log = log.Level(zerolog.InfoLevel)
	}

	go startMatrix(log)

	go startXonotic(log)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGHUP)

	log.Info().Msg("Initialization complete")

loop:
	for signal := range sigChan {
		log.Info().Str("signal", signal.String()).Msg("Signal received")
		switch signal {
		case os.Interrupt:
			break loop
		case syscall.SIGHUP:
			// TODO: reload config?
		}
	}

	if err := removeFromRcon(); err != nil {
		log.Error().Err(err).Msg("Unable to remove us from the UDP log")
	}

	// TODO: Anything else needed to gracefully close Matrix?
	mClient.StopSync()
}

func startXonotic(log zerolog.Logger) {
	err := addToRcon()
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to add us to the UDP log")
	}

	listenAddr := net.JoinHostPort(config.ListenAddress, strconv.Itoa(config.ListenPort))

	addr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		log.Fatal().Str("listenAddr", listenAddr).Msg("Could not resolve xonotic listen address")
	}

	conn, _ := net.ListenUDP("udp", addr)

	err = conn.SetReadBuffer(XonMSS)
	if err != nil {
		panic(err)
	}

	log.Debug().Str("address", addr.String()).Msg("Starting log processor")

	chatMessageRegex := regexp.MustCompile(`^\x01?\^\d(.+)\^7: (.+)`)

	for {
		err := conn.SetReadDeadline(time.Now().Add(1 * time.Minute))
		if err != nil {
			log.Error().Err(err).Msg("SetReadDeadline failed")

			continue
		}

		recvBuf := make([]byte, XonMSS)

		recvBufLen, _, err := conn.ReadFromUDP(recvBuf)
		if err != nil {
			var netError net.Error
			if ok := errors.As(err, &netError); ok && netError.Timeout() {
				log.Warn().Err(err).Msg("read timeout")

				err := addToRcon()
				if err != nil {
					log.Error().Err(err).Msg("Unable to add us to the UDP log")
				}
			} else {
				log.Error().Err(err).Msg("read error")
			}

			continue
		}

		packet := string(bytes.Trim(recvBuf[:recvBufLen], "\xFF"))

		log.Debug().Str("Addr", addr.String()).Msg(packet)

		messages := strings.Split(strings.TrimSpace(strings.TrimPrefix(packet, "n")), "\n")

		for _, message := range messages {
			log.Debug().Str("Addr", addr.String()).Str("Message", message).Msg("message")

			// Chat event
			if fields := chatMessageRegex.FindStringSubmatch(message); fields != nil {
				log.Debug().Strs("string", fields).Str("player", fields[1]).Str("mg", fields[2]).Msg("parsed message")
				msg := fmt.Sprintf("<%s>: %s", cleanXonoticText(fields[1]), cleanXonoticText(fields[2]))

				resp, err := mClient.SendText(mRoom.RoomID, msg)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send event")
				} else {
					log.Debug().Str("event_id", resp.EventID.String()).Msg("Event sent")
				}
			}
		}
	}
}

func startMatrix(log zerolog.Logger) {
	start := time.Now().UnixMilli()

	client, err := mautrix.NewClient(config.MatrixServer, id.UserID(config.MatrixUsername), config.MatrixToken)
	if err != nil {
		log.Fatal().Err(err).Str("server", config.MatrixServer).Str("username", config.MatrixUsername).Msg("Could not create a Matrix client")
	}

	mRoom, err = client.JoinRoom(config.MatrixRoom, "", nil)
	if err != nil {
		log.Fatal().Err(err).Str("room", config.MatrixRoom).Msg("Could not join Matrix room")
	}

	client.Log = log

	mClient = client

	syncer, ok := client.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		panic(err)
	}

	syncer.OnEventType(event.EventMessage, func(source mautrix.EventSource, evt *event.Event) {
		// FIXME: There has to be a better way to do these. Filters?
		// Possibly e.g. https://pkg.go.dev/maunium.net/go/mautrix@v0.16.1#Client.DontProcessOldEvents
		if start > evt.Timestamp {
			return // Ignore if buffered message
		}
		if evt.Type.Class != event.MessageEventType {
			return // Ignore if not a message event
		}
		if evt.RoomID != mRoom.RoomID {
			return // Ignore if not the room we care about
		}
		if evt.Sender == client.UserID {
			return // Ignore if self
		}

		sender := strings.Split(evt.Sender.URI().MXID1, ":")[0]
		message := evt.Content.AsMessage().Body

		log.Debug().
			Str("sender", sender).
			Str("type", evt.Type.String()).
			Str("id", evt.ID.String()).
			Str("body", message).
			Msg("Received message")

		msg := fmt.Sprintf(`settemp sv_adminnick "[M] %s"; say "%s"; settemp_restore sv_adminnick`, sender, message)

		res, err := execRcon(msg)
		if err != nil {
			log.Error().Err(err).Str("Rcon", msg).Str("Response", res).Msg("Failed to send message to Xonotic")
		}
	})

	err = client.Sync()
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err) // FIXME: Handle unexpected sync failure and update mClient
	}
}

var xonRegex = regexp.MustCompile(`(\^[0-9]|\^x[0-f]{3})`)

func cleanXonoticText(text string) string {
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		char, ok := qstr.XonoticDecodeKey[runes[i]]
		if ok {
			runes[i] = char
		}
	}

	return xonRegex.ReplaceAllString(string(runes), "")
}

func removeFromRcon() error {
	command := fmt.Sprint(`removefromlist log_dest_udp "`, config.ListenAddress, `:`, config.ListenPort, `"`)

	log.Info().Str("command", command).Msg("Removing us from log_dest_udp")

	_, err := execRcon(command)

	return err
}

func addToRcon() error {
	command := fmt.Sprint(`addtolist log_dest_udp "`, config.ListenAddress, `:`, config.ListenPort, `"`)

	log.Info().Str("command", command).Msg("Adding us to log_dest_udp")

	_, err := execRcon(command)

	return err
}

func execRcon(cmd string) (string, error) {
	reader, err := rconExecute(&xonServer, time.Now().Add(1*time.Second), cmd)
	if err != nil {
		return "", err
	}

	defer reader.Close()
	bytes, _ := io.ReadAll(reader)

	return string(bytes), nil
}

type Config struct {
	XonServer      string `json:"xon_server"`
	XonPort        int    `json:"xon_port"`
	ListenAddress  string `json:"listen_address"`
	ListenPort     int    `json:"listen_port"`
	RconPassword   string `json:"rcon_password"`
	RconMode       int    `json:"rcon_mode"`
	MatrixServer   string `json:"matrix_server"`
	MatrixUsername string `json:"matrix_username"`
	MatrixToken    string `json:"matrix_token"`
	MatrixRoom     string `json:"matrix_room"`
}

func getConfig() Config {
	var payload Config

	configLog := log.Fatal().Str("configFile", configFile)

	if configFile == "" {
		configLog.Msg("No config file specified, see -help")
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		configLog.Err(err).Msg("Couldn't open config file")
	}

	err = json.Unmarshal(content, &payload)
	if err != nil {
		configLog.Err(err).Msg("Unable to parse config file")
	}

	return payload
}
