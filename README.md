# gosmee - A webhook forwader/relayer

gosmee is a webhook forwarder that you can easily run anywhere.

## Description

Gosmee let you relays webhooks from itself (acting as a server) or from <https://smee.io> to your local notebook.

With `gosmee` you can easily expose the service on your local network or behind a VPN, letting a
public service (ie: GitHub) to push webhooks to it.

For example, if you setup your GitHub Webhook to point to a <https://smee.io/> URL or where `gosmee server` listen to.

You then use the `gosmee client` on your local notebook to get the events from the server and relay it to the local service. So effectively connecting github webhook to your local service on your local workstation.

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

### [Nix/NixOS](https://nixos.org/)

This repository includes a `flake` (see [NixOS Wiki on
Flakes](https://nixos.wiki/wiki/Flakes)).

If you have the `nix flake` command enabled (currenty on
nixos-unstable, `nixos-version` >= 22.05)

```shell
nix run github:chmouel/gosmee -- --help # your args are here
```

You can also use it to test and develop the source code:

```shell
nix develop # drops you in a shell with all the thing needed
nix flake check # runs tests
```
### System Services

System Service example file for macOS and Linux is available in the [misc](./misc) directory.

### Shell completion

Shell completion is available with:

```shell
# BASH
source <(gosmee completion bash)

# ZSH
source <(gosmee completion zsh)
```

## Usage

### Client

If you want to use <https://smee.io> you  may want to generate your own smee URL by going to <https://smee.io/new>.

When you have it, the basic usage is the following :

```shell
gosmee client https://smee.io/aBcDeF https://localhost:8080
```

It will replay all payload coming to to the smee URL on a service running on `http://localhost:8080`

Another option is to be able to save all the replay as a handy shell script :

```shell
gosmee client --saveDir /tmp/savedreplay https://smee.io/aBcDeF https://localhost:8080
```

What this will do is when you have a new payload comming to your smee URL, gosmee will save the json to
`/tmp/savedreplay/timestamp.json` and generate a shell script with curl options  to
`/tmp/savedreplay/timestamp.sh`. You then can simply replay the webhook at ease by
launching the shell script again and again..

You can ignore some events (if we detect it from Gitlab/GitHub/Bitbucket) if you add one or multiple `--ignore-event` flags.

You can add `--noReplay` if you only want the saving and not replaying.

You will have a pretty colored emoji unless you specify `--nocolor` as argument.

### Server

With `gosmee server` you can use your own server rather than <https://smee.io>
as relay. By default `gosmee server` will bind to `localhost` on port `3333`
which is not very useful. You probably want to expose it to your public IP or
behind a proxy with the flags `--address` and `--port`.

You really want to secure that endpoint, you can generate some letsencrypt
certificate and use the `--tls-cert` and `--tls-key` flags to specify them.

To use it you go to your URL and a suffix with your random ID. For example :

<https://myserverurl/RANDOM_ID>

The random ID accepted to the server needs to be at least 12 characters (and you
really want to be it random).

With `/new` you can easily generate a random ID, ie:

```shell
% curl http://localhost:3333/new
http://localhost:3333/NqybHcEi
```

### Nginx

Running gosmee server behind nginx may require some configuration to work properly.
Here is a `proxy_pass location` to a locally running gosmee server on port localhost:3333:

```nginx
    location / {
        proxy_pass         http://127.0.0.1:3333;
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header Connection '';
        proxy_http_version 1.1;
        chunked_transfer_encoding off;
    }
```

### Kubernetes

You can expose an internal kubernetes deployment or service with gosmee  by using [this file](./misc/kubernetes-deployment.yaml)

Adjust the SMEE_URL in there to your endpoint

and the `http://deployment.name.namespace.name:PORT_OF_SERVICE` URL is the Kubernetes internal URL of your deployment running on your cluster, for example :

   <http://service.namespace:8080>

## Thanks

- Most of the works is done by the [go-sse](https://github.com/r3labs/sse) library.
- Used previously [pysmee](https://github.com/akrog/pysmee) but it seems that the underlying sse library is broken with chunked transfer.

## Copyright

[Apache-2.0](./LICENSE)

## Authors

Chmouel Boudjnah <[@chmouel](https://twitter.com/chmouel)>
