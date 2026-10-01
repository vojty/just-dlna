FROM node:24-alpine AS ui
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json vite.config.ts ./
COPY ui ./ui
RUN npm run build

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
 && mkdir /cache /config && chown just-dlna /cache /config
COPY --from=build /just-dlna /usr/local/bin/just-dlna
COPY --from=ui /src/ui/dist /usr/share/just-dlna/ui
USER just-dlna
# Only settings that differ from the built-in defaults are set here: the web
# UI cannot change settings fixed by environment variables.
ENV MEDIA_PATH=/media \
    CACHE_DIR=/cache \
    CONFIG_FILE=/config/just-dlna.yaml \
    UI_DIR=/usr/share/just-dlna/ui
VOLUME ["/cache", "/config"]
EXPOSE 1338/tcp 1339/tcp 1340/tcp 1900/udp
ENTRYPOINT ["/usr/local/bin/just-dlna"]
