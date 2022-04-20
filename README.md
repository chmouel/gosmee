# gosmee - smee.io go client

A command line client for Smee’s webhook payload delivery service in GO.

## Description

Replay message from <https://smee.io/> to a target service. Allowing you to
easily expose a local dev service to the internet to be consumed by webhooks.

## Screenshot

![Screenshot](./.github/screenshot.png)

## Install

### Release

Go to the [release](https://github.com/chmouel/gosmee/releases) page and choose your archive or package for your platform.

## Homebrew

```shell
brew tap chmouel/gosmee https://github.com/chmouel/gosmee
brew install gosmee
```

## [Arch](https://aur.archlinux.org/packages/gosmee-bin)

```shell
yay -S gosmee-bin
```

### [Docker](https://github.com/users/chmouel/packages/container/package/gosmee)

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

System Service example file for macOS and Linux is available in the [misc](./misc) directory.

## Usage

You first may want to generate your own smee URL by going to <https://smee.io/new>

When you have it the basic usage is the folllowing :

```shell
gosmee https://smee.io/aBcDeF https://localhost:8080
```

this will replay all payload comingto to the smee URL on a service running on `http://localhost:8080`

Another option is to be able to save all the replay as a handy shell script :

```shell
gosmee --saveDir /tmp/savedreplay https://smee.io/aBcDeF https://localhost:8080
```

What this will do is when you have a new payload comming to your smee URL, gosmee will save the json to
`/tmp/savedreplay/timestamp.json` and generate a shell script with curl options  to
`/tmp/savedreplay/timestamp.sh`. You then can simply replay the webhook at ease by
launching the shell script again and again..

You can ignore some events (if we detect it from Gitlab/GitHub/Bitbucket) if you add one or multiple `--ignore-event` flags.

You can add `--noReplay` if you only want the saving and not replaying.

You will have a pretty colored emoji unless you specify `--nocolor` as argument.

## Thanks

- Most of the works is done by the [go-sse](github.com/r3labs/sse) library.
- Used previously [pysmee](https://github.com/akrog/pysmee) but it seems that the underlying sse library is broken with chunked transfer.

## Copyright

[Apache-2.0](./LICENSE)

## Authors

Chmouel Boudjnah <[@chmouel](https://twitter.com/chmouel)>
