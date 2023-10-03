# rcon2matrix

STATUS: MVP

This repo contains a Go application that will relay chat messages between a Xonotic server and a Matrix room.

Contains [code](rcon.go) from the apparently unlicensed [TheRegulars/website](https://github.com/TheRegulars/website/) backend (thanks @bacher09! 💙)

# How To Use

## Configure

- Copy [`config.template.json`](config.template.json) to `config.json`
- Edit `config.json` to include the details of your Xonotic server and Matrix room

## Run From Github

- Download the latest build artifact binary from [`garymoon/rcon2matrix`](https://github.com/garymoon/rcon2matrix/actions/workflows/go.yml) to the same directory as `config.json`
- Mark it executable: `chmod +x rcon2matrix`
- Run rcon2matrix: `./rcon2matrix -config config.json`

## Build and Run with Docker

- Build the Docker image: `docker build -t rcon2matrix .`
- Run rcon2matrix: `docker run -v "$(pwd)/config.json:/config.json:ro" rcon2matrix -config config.json`

## Build and Run Locally

- Depends on [Golang v1.21 or above](https://go.dev/doc/install)
- Clone this repository and `cd` into it
- Move `config.json` into the repo working copy
- Download deps and build: `go mod download && go mod verify && go build -v ./...`
- Run rcon2matrix: `./rcon2matrix -config config.json`
