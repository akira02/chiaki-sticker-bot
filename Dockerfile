# Stage 1: Build React WebApp
FROM node:18-bookworm-slim AS webapp-builder

WORKDIR /webapp
COPY web/webapp3/package.json web/webapp3/package-lock.json ./
RUN npm ci
COPY web/webapp3/ ./
RUN PUBLIC_URL=/webapp npm run build

# Stage 2: Build Go binary
FROM golang:1.22-bookworm AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /moe-sticker-bot ./cmd/moe-sticker-bot/main.go

# Stage 3: Fetch a static ffmpeg with the animated WebP demuxer (FFmpeg 9.0+).
# Debian only packages 5.1 (bookworm) / 7.1 (trixie) / 8.1 (sid), none of which
# can decode animated WebP at all -- they skip the ANMF chunks. Pinned to a
# dated autobuild because BtbN publishes no n9.0 release assets yet, so the
# only 9.x option is a master snapshot.
FROM debian:bookworm-slim AS ffmpeg-fetcher

ARG FFMPEG_BUILD_TAG=autobuild-2026-08-04-21-26
ARG FFMPEG_ASSET=ffmpeg-N-125954-g9862dd83b1-linux64-lgpl.tar.xz

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/ffmpeg.tar.xz \
      "https://github.com/BtbN/FFmpeg-Builds/releases/download/${FFMPEG_BUILD_TAG}/${FFMPEG_ASSET}" \
    && mkdir -p /ffmpeg \
    && tar -xJf /tmp/ffmpeg.tar.xz -C /ffmpeg --strip-components=2 --wildcards '*/bin/ffmpeg' '*/bin/ffprobe' \
    && rm /tmp/ffmpeg.tar.xz

# Fail the build rather than ship a binary that silently mistimes every
# animated WebP: the converter's frame-delay probing falls back quietly.
RUN /ffmpeg/ffmpeg -hide_banner -demuxers | grep -q webp_anim \
    && /ffmpeg/ffmpeg -hide_banner -encoders | grep -q libwebp \
    && /ffmpeg/ffmpeg -hide_banner -encoders | grep -q libvpx-vp9 \
    && /ffmpeg/ffprobe -hide_banner -demuxers | grep -q webp_anim

# Stage 4: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    imagemagick \
    libarchive-tools \
    ffmpeg \
    cpulimit \
    curl \
    gifsicle \
    python3 \
    python3-pip \
    ca-certificates \
    nginx \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install --break-system-packages rlottie-python emoji pillow

COPY tools/msb_emoji.py /usr/local/bin/msb_emoji.py
COPY tools/msb_kakao_decrypt.py /usr/local/bin/msb_kakao_decrypt.py
COPY tools/msb_rlottie.py /usr/local/bin/msb_rlottie.py
RUN chmod +x /usr/local/bin/msb_emoji.py /usr/local/bin/msb_kakao_decrypt.py /usr/local/bin/msb_rlottie.py

# /usr/local/bin precedes /usr/bin in PATH, so this shadows the apt ffmpeg that
# the rest of the image still pulls in. Removing these two lines reverts to it.
COPY --from=ffmpeg-fetcher /ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-fetcher /ffmpeg/ffprobe /usr/local/bin/ffprobe

COPY --from=go-builder /moe-sticker-bot /usr/local/bin/moe-sticker-bot
COPY --from=webapp-builder /webapp/build /webapp/build

COPY web/nginx/fly.conf /etc/nginx/conf.d/default.conf
RUN rm -f /etc/nginx/sites-enabled/default

COPY start-bot.sh /usr/local/bin/start-bot.sh
RUN chmod +x /usr/local/bin/start-bot.sh

VOLUME ["/data"]

EXPOSE 8080

CMD ["/usr/local/bin/start-bot.sh"]
