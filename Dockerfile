FROM --platform=$BUILDPLATFORM golang:latest
COPY . /go/src/github.com/aaamosljp/gosmee
WORKDIR /go/src/github.com/aaamosljp/gosmee
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a  -ldflags="-s -w"  -installsuffix cgo -o /tmp/gosmee .

FROM registry.access.redhat.com/ubi9/ubi-minimal
RUN microdnf -y update && microdnf -y --nodocs install tar rsync shadow-utils && microdnf clean all && useradd gosmee && rm -rf /var/cache/yum

COPY --from=0 /tmp/gosmee /usr/local/bin/gosmee

WORKDIR /home/gosmee
USER gosmee
ENTRYPOINT ["/usr/local/bin/gosmee"]
