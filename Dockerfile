FROM golang:1.26 AS build

WORKDIR /opt/etcdkeeper
COPY . .
WORKDIR /opt/etcdkeeper/src/etcdkeeper
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o etcdkeeper.bin .


FROM alpine:3.22

ENV HOST="0.0.0.0"
ENV PORT="8080"

RUN adduser -D -H -u 10001 etcdkeeper
WORKDIR /opt/etcdkeeper
COPY --from=build /opt/etcdkeeper/src/etcdkeeper/etcdkeeper.bin .
COPY assets assets
USER etcdkeeper

EXPOSE ${PORT}

CMD ./etcdkeeper.bin -h $HOST -p $PORT
