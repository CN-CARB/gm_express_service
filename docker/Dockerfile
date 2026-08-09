FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /src
COPY ./goserver/go.mod ./
COPY ./goserver/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /express .

FROM scratch
COPY --from=build /express /express
USER 65532:65532
EXPOSE 3000
ENTRYPOINT ["/express"]
