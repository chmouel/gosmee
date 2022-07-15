# gosmee - smee.io go client

A command line client for Smee’s webhook payload delivery service in GO.

## Description

Replay message from <https://smee.io/> to a target host. Allowing you to
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

### Shell completion

Shell completion is available with:

```shell
# BASH
source <(gosmee completion bash)

# ZSH
source <(gosmee completion zsh)
```

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

## Build image for your arch 

```
update GOARCH to your arch in Dockerfile, such as GOARCH=s390x
podman build -t yourrepo/gosmee:s390x .
podman push yourrepo/gosmee:s390x
```

## Deploy it on openshift

```
kind: Deployment
apiVersion: apps/v1
metadata:
  name: gosmee-client
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gosmee-client
  template:
    metadata:
      creationTimestamp: null
      labels:
        app: gosmee-client
    spec:
      containers:
        - name: gosmee-client
          image: 'docker.io/zhengxiaomei123/gosmee:s390x'
          args:
            - 'https://smee.io/yHv1dESyg7BJmm4i'
            - $(SVC)
          env:
            - name: SVC
              value: >-
                http://pipelines-as-code-controller-openshift-pipelines.apps.pli43-tt4.psi.ospqa.com
      restartPolicy: Always
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 25%
      maxSurge: 25%
  revisionHistoryLimit: 10
  progressDeadlineSeconds: 600
```

## Thanks

- Most of the works is done by the [go-sse](github.com/r3labs/sse) library.
- Used previously [pysmee](https://github.com/akrog/pysmee) but it seems that the underlying sse library is broken with chunked transfer.

## Copyright

[Apache-2.0](./LICENSE)

## Authors

Chmouel Boudjnah <[@chmouel](https://twitter.com/chmouel)>
