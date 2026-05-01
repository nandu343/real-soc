# Multi-stage build for the proxy agent
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY . .
RUN apk add --no-cache git && \
    CGO_ENABLED=0 go build -o /bin/proxy-agent ./main.go

FROM alpine:3.18
COPY --from=build /bin/proxy-agent /bin/proxy-agent
EXPOSE 8080
ENTRYPOINT ["/bin/proxy-agent"]
