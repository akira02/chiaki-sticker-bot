# [@chiaki_sticker_bot](https://t.me/chiaki_sticker_bot)

A self-hosted Telegram sticker bot, forked from [@moe_sticker_bot](https://github.com/star-39/moe-sticker-bot) by [star-39](https://github.com/star-39).

> **Why this fork?**
> The original [@moe_sticker_bot](https://t.me/moe_sticker_bot) has been offline for over 2 years and the upstream repository is no longer maintained. We love what star-39 built, so we're forking it to keep it alive — with a focus on running efficiently on low-RAM, low-cost hosting like [fly.io](https://fly.io).

---

## Features

* Import LINE or Kakao sticker packs to Telegram, with batch or individual emoji assignment.
* Create your own sticker set or CustomEmoji from images or videos in any format.
* Support mixed-format sticker sets (animated + static in the same set).
* Batch download Telegram stickers or GIFs, auto-converted to common formats.
* Manage sticker sets interactively via WebApp: add / move / remove / edit emoji.
* Provides a CLI tool [msbimport](./pkg/msbimport) for downloading LINE/Kakao stickers.

* 輕鬆匯入LINE/Kakao貼圖包到Telegram, 可以統一或分開指定emoji.
* 使用任意格式的圖片和影片創建自己的貼圖包或表情貼.
* 支援混合貼圖包（動態與靜態貼圖可放在同一個包內）。
* 下載Telegram/LINE/Kakao貼圖包和GIF，自動轉換為常用格式。
* 互動式WebApp管理貼圖包：新增/刪除/移動貼圖或修改Emoji。

---

## Deployment

### Docker / Docker Compose

The included `Dockerfile` contains all build and runtime dependencies. It runs
the bot, the WebApp, and nginx on port `8080`; persistent bot data lives in
`/data`.

```bash
# 1. Configure the bot
cp .env.example .env
# Edit .env and set at least BOT_TOKEN.

# 2. Build and start it
docker compose up -d --build
```

For webhook mode, put the container behind a reverse proxy that serves HTTPS
and set `WEBHOOK_URL` to its public `https://.../webhook` URL. Set `WEBAPP_URL`
to the corresponding public `https://.../webapp` URL only if the WebApp should
be enabled. Leaving `WEBHOOK_URL` unset uses Telegram long polling instead.

To run without Compose:

```bash
docker build -t moe-sticker-bot .
docker run -d --name moe-sticker-bot \
  -p 8080:8080 \
  --env-file .env \
  -v moe-sticker-bot-data:/data \
  --restart unless-stopped \
  moe-sticker-bot
```

### Fly.io

This fork is designed to run on [fly.io](https://fly.io) with 256MB RAM.

Uses **webhook mode** with **blue-green deploys** so in-flight sticker imports are not interrupted during updates.

### Prerequisites

* [flyctl](https://fly.io/docs/hands-on/install-flyctl/) installed and authenticated
* A Telegram bot token from [@BotFather](https://t.me/BotFather)

### Steps

```bash
# 1. Fork and clone this repo
git clone https://github.com/akira02/chiaki-sticker-bot && cd chiaki-sticker-bot

# 2. Create the fly.io app
fly launch --no-deploy

# 3. Set secrets
fly secrets set \
  BOT_TOKEN="your-bot-token" \
  WEBHOOK_URL="https://your-app-name.fly.dev/webhook" \
  WEBHOOK_SECRET="$(openssl rand -hex 32)"

# 4. Deploy
fly deploy
```

The bot listens on `:8080` for both the Telegram webhook (`POST /webhook`) and the fly.io health check (`GET /health`).

### Low-memory tuning

The default settings are conservative for 256MB fly.io machines. Animated LINE/Kakao imports are memory-heavy because they use ImageMagick and ffmpeg/VP9.

The built-in defaults are:

* `MSB_IMPORT_CONCURRENCY` — maximum number of sticker import jobs running at once. Default: `1`. Keep this at `1` on 256MB machines; raising it can cause OOM when multiple users import animated packs.
* `MSB_FFMPEG_CONCURRENCY` — maximum number of ffmpeg processes running at once. Default: `1`.
* `MSB_IM_MEMORY_LIMIT` — ImageMagick memory limit for animated conversion paths. Default: `64MiB`. Set to `0` to disable.
* `MSB_IM_MAP_LIMIT` — ImageMagick memory-map limit for animated conversion paths. Default: `128MiB`. Set to `0` to disable.
* `MSB_IM_OOM_MEMORY_LIMIT` — lower ImageMagick memory limit used for one retry after ImageMagick or a paired pipe conversion is killed. Default: `32MiB`. Set to `0` to disable.
* `MSB_IM_OOM_MAP_LIMIT` — lower ImageMagick memory-map limit used for that OOM retry. Default: `64MiB`. Set to `0` to disable.
* `MSB_KAKAO_FAST_PIPE` — set to `1` to use the faster one-pass Kakao animated WebP pipe path. Default is off; the default two-pass frame-sequence path is slower but gives better motion quality under Telegram's 255KiB video sticker limit.

The checked-in Fly config pins `MSB_IM_MEMORY_LIMIT=32MiB` and `MSB_IM_MAP_LIMIT=64MiB` on the 256MB VM, with a one-shot OOM retry at `24MiB` / `48MiB`.
It also sets `GOMEMLIMIT=128MiB`, limits heavy encoders to 70% of one CPU, and
uses five-minute conversion timeouts. These values are Fly.io-specific tuning,
not Docker requirements; adjust them for the memory and CPU available to your
own host.


### Optional: Enable database (for /search and usage tracking)

Use a MySQL-compatible service such as [TiDB Cloud Serverless](https://tidbcloud.com) (free tier available):

```bash
fly secrets set \
  DB_ADDR="your-host:4000" \
  DB_USER="your-user" \
  DB_PASS="your-password"
```

> Note: TiDB Cloud requires TLS. This fork has TLS enabled by default in the DB connection.

### Optional: Enable WebApp (/manage with visual editor)

Set `WEBAPP_URL` to the public HTTPS URL of `/webapp`. The WebApp API is served
on the same `:8080` port as the webhook, and its data is stored in
`/data/webapp` by default.

---

## System Dependencies

The Dockerfile in this repo includes all required dependencies. For manual builds:

* ImageMagick (6 or 7)
* bsdtar (libarchive-tools)
* ffmpeg
* curl, gifsicle
* python3 with `emoji`, `rlottie-python`, and `pillow` packages

---

## Build from source

```bash
git clone https://github.com/akira02/chiaki-sticker-bot && cd chiaki-sticker-bot
go build -o chiaki-sticker-bot cmd/moe-sticker-bot/main.go
```

---

## Maintenance goals

- Keep memory usage low enough to run on fly.io free/hobby tier (256MB target)
- Stay compatible with upstream Telegram Bot API changes
- Avoid feature bloat — core sticker import/export/manage functionality only

---

## Credits

* Original project: [star-39/moe-sticker-bot](https://github.com/star-39/moe-sticker-bot) — GPL v3
* [blluv/KakaoTalkEmoticonDownloader](https://github.com/blluv/KakaoTalkEmoticonDownloader) MIT License
* [laggykiller/rlottie-python](https://github.com/laggykiller/rlottie-python) GPL-2.0

## License

GPL v3 — see [LICENSE](./LICENSE)
