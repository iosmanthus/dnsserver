FROM golang:1.17-alpine

ENV GOPATH /go
RUN ls /go
RUN apk add --no-cache \
    git
RUN mkdir -p /dnsserver
COPY . /dnsserver
WORKDIR /dnsserver
RUN go get && go build
EXPOSE 53 53/udp
ENTRYPOINT ["/dnsserver/dnsserver"]
