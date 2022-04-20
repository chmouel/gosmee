# gosmee - smee.io go client

A command line client for Smee’s webhook payload delivery service in GO.

## Description

Replay message from <https://smee.io/> to a target service. Allowing you to
easily expose some local developement server to the internet for webhook

## Install

### Release

Go to the [release](https://github.com/chmouel/gosmee/releases) page and choose your archive or package for your platform.

## Homebrew

```shell
brew tap chmouel/gosmee https://github.com/chmouel/gosmee
brew install gosmee
```

### Docker

```shell
docker run ghcr.io/chmouel/gosmee:latest
```

## GO

```shell
go install -v github.com/chmouel/gosmee@latest
```

### Git

Checkout the directory and use :

```shell
-$ make build
-$ ./bin/gosmee --help
```

### System Services

System Service example file for macOS and Linux is available in the [./hack/](./hack) directory.

## Usage

The basic usage is with a smee URL and a target URL example :

```shell
gosmee https://smee.io/aBcDeF https://localhost:8080
```

will replay all message from the smee URL to a service on `localhost:8080`

Another option is to be able to save all the replay as shell script :

```shell
gosmee --saveDir /tmp/savedreplay https://smee.io/aBcDeF https://localhost:8080
```

When you have a new message comming to your smee URL gosmee will save the json to
`/tmp/savedreplay/timestamp.json` and a shell script with curl to
`/tmp/savedreplay/timestamp.sh`, you can simply replay the webhook at ease by
launching the shell script.

You can add `--noReplay` if you only want the saving and not replaying.

You will have a pretty colored emoji unless you specify `--nocolor` as argument.

## Thanks

- Most of the works is done by the [go-sse](github.com/r3labs/sse) library.
- Used previously [pysmee](https://github.com/akrog/pysmee) but it seems that the underlying sse library is broken with chunked transfer. 

## Copyright

[Apache-2.0](./LICENSE)

## Authors

Chmouel Boudjnah <[@chmouel](https://twitter.com/chmouel)>
