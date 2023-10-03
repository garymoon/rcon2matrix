FROM golang:latest AS builder

WORKDIR /rcon2matrix

COPY go.mod go.sum ./

RUN go mod download && go mod verify

COPY . .

RUN go build -ldflags="-s -w" -trimpath -v ./...

###

FROM debian:latest

COPY --from=builder /rcon2matrix/rcon2matrix /usr/local/bin/rcon2matrix

ENTRYPOINT ["/usr/local/bin/rcon2matrix"]
