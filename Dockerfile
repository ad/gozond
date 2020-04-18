FROM golang:alpine as builder
WORKDIR /app
COPY . .
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -v -o /go/bin/app

FROM alpine:3.10

RUN apk add --no-cache --update ca-certificates tzdata bash coreutils \
    && cp /usr/share/zoneinfo/Europe/Moscow /etc/localtime \
    && echo "Europe/Moscow" >  /etc/timezone \
    && apk del tzdata \
    && rm -rf /var/cache/apk/*

EXPOSE 80
WORKDIR /app/

COPY --from=builder /go/bin/app ./

ENTRYPOINT ["/bin/sh", "-c"]

CMD ["./app"]
