FROM arm32v7/golang:stretch

COPY qemu-arm-static /usr/bin/
WORKDIR /go/src/github.com/automatedhome/heater
COPY . .
RUN go build -o heater cmd/main.go

FROM arm32v7/busybox:1.30-glibc

COPY --from=0 /go/src/github.com/automatedhome/heater/heater /usr/bin/heater

ENTRYPOINT [ "/usr/bin/heater" ]
