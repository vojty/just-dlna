FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /just-dlna .

FROM alpine:3
# ffmpeg/ffprobe are only used to read media info and convert subtitles;
# video is always streamed as-is.
RUN apk add --no-cache ffmpeg tzdata \
 && adduser -D -H -u 1000 just-dlna \
 && mkdir /cache && chown just-dlna /cache
COPY --from=build /just-dlna /usr/local/bin/just-dlna
USER just-dlna
ENV MEDIA_PATH=/media \
    CACHE_DIR=/cache \
    HTTP_PORT=1338 \
    MEDIA_PORT=1339 \
    LOG_LEVEL=info
VOLUME ["/cache"]
EXPOSE 1338/tcp 1339/tcp 1900/udp
ENTRYPOINT ["/usr/local/bin/just-dlna"]
