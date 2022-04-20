# gosmee  - smee.io go client

A command line client for Smee’s webhook payload delivery service in GO.

## Description

Replay message from <https://smee.io/> to a target service. Allowing you to
easily expose some local developement server to the internet for webhook

## Install

### Release

Go to  the [release](https://github.com/chmouel/gosmee/releases) page and choose your tarball, package for your platform.

### Docker

```
docker run ghcr.io/chmouel/gosmee:latest
```

### GIT

Checkout the directory and use :
```shell
-$ make build
-$ ./bin/gosmee --help
```

## Usage

The basic usage is with a smee url and a target url example :

```shell
gosmee https://smee.io/aBcDeF https://localhost:8080
```

will replay all message from the smee url to a service on `localhost:8080`

Another option is to be able to save all the replay as shell script :

```shell
gosmee --saveDir /tmp/savedreplay https://smee.io/aBcDeF https://localhost:8080
```

When you have a new message comming to your smee url gosmee will save the json to
`/tmp/savedreplay/timestamp.json` and a shell script with curl to
`/tmp/savedreplay/timestamp.sh`, you can simply replay the webhook at ease by
launching the shell script.

You can add `--noReplay` if you only want the saving and not replaying.

## Thanks

Most of the works is done by the [go-sse](github.com/r3labs/sse) library

## Copyright

[Apache-2.0](./LICENSE)

## Authors

Chmouel Boudjnah <[@chmouel](https://twitter.com/chmouel)>
