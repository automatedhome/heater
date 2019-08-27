FROM arm32v7/golang:stretch as builder

COPY qemu-arm-static /usr/bin/
WORKDIR /go/src/github.com/automatedhome/heater
COPY . .
RUN make build

FROM arm32v7/busybox:1.30-glibc

COPY --from=builder /go/src/github.com/automatedhome/heater/heater /usr/bin/heater
COPY --from=builder /go/src/github.com/automatedhome/heater/config.yaml /config.yaml

ENTRYPOINT [ "/usr/bin/heater" ]
