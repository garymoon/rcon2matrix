// Shamelessly lifted from https://github.com/TheRegulars/website/tree/master/backend/pkg/rcon (for now)
// Thank you Slava! <3

package main

import (
	"bytes"
	"crypto/hmac"
	"io"
	"net"
	"strconv"
	"time"

	//nolint:staticcheck // There isn't any choice, md4 is what DP rcon uses
	"golang.org/x/crypto/md4"
)

const (
	// rconNonSecureMode       = 0
	rconTimeSecureMode      = 1
	rconChallengeSecureMode = 2
	XonMSS                  = 1460
)

type ServerConfig struct {
	Server       string `json:"server" yaml:"server"`
	Port         int    `json:"port" yaml:"port"`
	RconPassword string `json:"rcon_password" yaml:"rcon_password"`
	RconMode     int    `json:"rcon_mode" yaml:"rcon_mode"`
}

type rconReader struct {
	conn  net.Conn
	buf   []byte
	slice []byte
}

func (reader *rconReader) Read(buffer []byte) (int, error) {
	if len(reader.slice) == 0 {
		for {
			n, err := reader.conn.Read(reader.buf)
			if err != nil {
				return 0, err
			}

			if bytes.HasPrefix(reader.buf[:n], []byte(RconResponseHeader)) {
				reader.slice = reader.buf[len(RconResponseHeader):n]

				break
			}
		}
	}

	num := copy(buffer, reader.slice)
	reader.slice = reader.slice[num:len(reader.slice)]

	return num, nil
}

func (reader *rconReader) Close() error {
	return reader.conn.Close()
}

func rconExecute(server *ServerConfig, deadline time.Time, cmd string) (io.ReadCloser, error) {
	var (
		challenge    []byte
		outputBuffer bytes.Buffer
	)

	addr := net.JoinHostPort(server.Server, strconv.Itoa(server.Port))

	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}

	err = conn.SetDeadline(deadline)
	if err != nil {
		panic(err)
	}

	readBuffer := make([]byte, XonMSS)

	//nolint:nestif // Ugh
	if server.RconMode == rconChallengeSecureMode {
		_, err := conn.Write([]byte(ChallengeRequest))
		if err != nil {
			conn.Close()

			return nil, err
		}

		for {
			// read until we receive challenge response
			readLen, err := conn.Read(readBuffer)
			if err != nil {
				conn.Close()

				return nil, err
			}

			if bytes.HasPrefix(readBuffer[:readLen], []byte(ChallengeHeader)) {
				challengeEnd := len(ChallengeHeader)

				for i := len(ChallengeHeader); i < readLen; i++ {
					if readBuffer[i] == '\x00' {
						challengeEnd = i

						break
					}
				}

				challenge = readBuffer[len(ChallengeHeader):challengeEnd]

				break
			}
		}
		RconSecureChallengePacket(cmd, server.RconPassword, challenge, &outputBuffer)
	} else if server.RconMode == rconTimeSecureMode {
		RconSecureTimePacket(cmd, server.RconPassword, time.Now(), &outputBuffer)
	} else {
		RconNonSecurePacket(cmd, server.RconPassword, &outputBuffer)
	}

	_, err = conn.Write(outputBuffer.Bytes())
	if err != nil {
		conn.Close()

		return nil, err
	}

	return &rconReader{conn: conn, buf: readBuffer, slice: nil}, nil
}

const (
	QHeader            string = "\xFF\xFF\xFF\xFF"
	RconResponseHeader string = QHeader + "n"
	ChallengeRequest   string = QHeader + "getchallenge"
	ChallengeHeader    string = QHeader + "challenge "
	// PingPacket         string = QHeader + "ping"
	// PingResponse       string = QHeader + "ack"
)

func RconNonSecurePacket(command string, password string, buf *bytes.Buffer) {
	buf.WriteString(QHeader)
	buf.WriteString("rcon ")
	buf.WriteString(password)
	buf.WriteString(" ")
	buf.WriteString(command)
}

func RconSecureTimePacket(command string, password string, ts time.Time, buf *bytes.Buffer) {
	mac := hmac.New(md4.New, []byte(password))
	t := float64(ts.UnixNano()) / float64(time.Second/time.Nanosecond)
	timeStr := strconv.FormatFloat(t, 'f', 6, 64)
	mac.Write([]byte(timeStr))
	mac.Write([]byte(" "))
	mac.Write([]byte(command))
	buf.WriteString(QHeader)
	buf.WriteString("srcon HMAC-MD4 TIME ")
	buf.Write(mac.Sum(nil))
	buf.WriteString(" ")
	buf.WriteString(timeStr)
	buf.WriteString(" ")
	buf.WriteString(command)
}

func RconSecureChallengePacket(command string, password string, challenge []byte, buf *bytes.Buffer) {
	mac := hmac.New(md4.New, []byte(password))
	mac.Write(challenge)
	mac.Write([]byte(" "))
	mac.Write([]byte(command))
	buf.WriteString(QHeader)
	buf.WriteString("srcon HMAC-MD4 CHALLENGE ")
	buf.Write(mac.Sum(nil))
	buf.WriteString(" ")
	buf.Write(challenge)
	buf.WriteString(" ")
	buf.WriteString(command)
}
