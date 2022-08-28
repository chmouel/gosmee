FROM mirror.gcr.io/library/golang:latest
COPY . /go/src/github.com/chmouel/gosmee
WORKDIR /go/src/github.com/chmouel/gosmee
RUN CGO_ENABLED=0 GOOS=linux go build -a  -ldflags="-s -w"  -installsuffix cgo -o gosmee .

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.5-240.1648458092
RUN microdnf update \
 && microdnf install --nodocs rsync \
 && microdnf clean all \
 && rm -rf /var/cache/yum

COPY --from=0 /go/src/github.com/chmouel/gosmee/gosmee /usr/local/bin/gosmee

WORKDIR /
ENTRYPOINT ["/usr/local/bin/gosmee"]
