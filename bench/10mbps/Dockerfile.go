FROM golang:1.26-alpine AS build
WORKDIR /src
COPY goserver/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /express-go .

FROM alpine:latest
RUN apk add --no-cache iproute2
COPY --from=build /express-go /express-go
ENTRYPOINT ["/bin/sh", "-ec"]
CMD ["tc qdisc add dev eth0 root tbf rate ${BENCH_RATE:-10mbit} burst 16kb latency 500ms; exec /express-go"]
