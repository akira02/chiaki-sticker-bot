package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-co-op/gocron"
	log "github.com/sirupsen/logrus"
	"github.com/star-39/moe-sticker-bot/pkg/msbimport"
	"golang.org/x/time/rate"
	tele "gopkg.in/telebot.v3"
)

func Init(conf ConfigTemplate) {
	msbconf = conf
	initLogrus()
	msbimport.InitConvert()
	b = initBot(conf)

	// Start HTTP server immediately so fly.io health checks pass
	// while DB and other slow init (TiDB wakeup) happen in the background.
	srv := initHTTPServer()

	initWorkspace(b)
	initWorkersPool()
	go initGoCron()

	// complies to telebot v3.1
	b.Use(Recover())

	b.Handle("/quit", cmdQuit)
	b.Handle("/cancel", cmdQuit)
	b.Handle("/exit", cmdQuit)
	b.Handle("/faq", cmdFAQ)
	b.Handle("/changelog", cmdChangelog)
	b.Handle("/privacy", cmdPrivacy)
	b.Handle("/help", cmdStart)
	b.Handle("/about", cmdAbout)
	b.Handle("/command_list", cmdCommandList)
	b.Handle("/import", cmdImport, checkState)
	b.Handle("/download", cmdDownload, checkState)
	b.Handle("/create", cmdCreate, checkState)
	b.Handle("/manage", cmdManage, checkState)
	b.Handle("/search", cmdSearch, checkState)

	// b.Handle("/register", cmdRegister, checkState)
	b.Handle("/sitrep", cmdSitRep, checkState)
	b.Handle("/getfid", cmdGetFID, checkState)

	b.Handle("/start", cmdStart, checkState)

	b.Handle(tele.OnText, handleMessage)
	b.Handle(tele.OnVideo, handleMessage)
	b.Handle(tele.OnAnimation, handleMessage)
	b.Handle(tele.OnSticker, handleMessage)
	b.Handle(tele.OnDocument, handleMessage)
	b.Handle(tele.OnPhoto, handleMessage)
	b.Handle(tele.OnCallback, handleMessage, autoRespond, sanitizeCallback)

	// Set up signal handler
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	// Start bot in goroutine (non-blocking)
	go b.Start()
	log.WithFields(log.Fields{"botName": botName, "dataDir": dataDir}).Info("Bot OK.")

	// Block until signal
	<-quit
	log.Info("SIGTERM received, draining active sessions...")
	shuttingDown.Store(true)

	// Shutdown HTTP server first so no new Telegram webhooks are accepted.
	// We intentionally don't call b.Stop() — telebot v3.99.9 has a
	// "close of closed channel" panic in Webhook.waitForStop on shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	failActiveSessionsForShutdown()

	done := make(chan struct{})
	go func() {
		activeSessionsWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Info("All sessions completed, shutting down cleanly.")
	case <-time.After(5 * time.Minute):
		log.Warn("Shutdown timeout (5m), forcing exit.")
	}
}

// Recover returns a middleware that recovers a panic happened in
// the handler.
func Recover(onError ...func(error)) tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			var f func(error)
			if len(onError) > 0 {
				f = onError[0]
			} else {
				f = func(err error) {
					c.Bot().OnError(err, c)
				}
			}

			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						f(err)
					} else if s, ok := r.(string); ok {
						f(errors.New(s))
					}
				}
			}()

			return next(c)
		}
	}
}

// This one never say goodbye.
func endSession(c tele.Context) {
	cleanUserDataAndDir(c.Sender().ID)
}

// This one will say goodbye.
func terminateSession(c tele.Context) {
	cleanUserDataAndDir(c.Sender().ID)
	c.Send("Bye. /start")
}

func endManageSession(c tele.Context) {
	users.mu.Lock()
	ud, exist := users.data[c.Sender().ID]
	users.mu.Unlock()
	if !exist {
		return
	}
	if ud.stickerData.id == "" {
		return
	}
	path := filepath.Join(msbconf.WebappDataDir, ud.stickerData.id)
	os.RemoveAll(path)
}

// Transient network errors (connection reset, broken pipe, EOF, i/o timeout)
// are common when talking to the Telegram API and recover on their own.
func isTransientNetworkError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "timeout") || strings.Contains(errText, "deadline exceeded")
}

func onError(err error, c tele.Context) {
	var apiErr *tele.Error
	switch {
	case errors.Is(err, context.Canceled):
		// Expected when a user cancels an in-flight import/conversion.
		log.Debugln("Context canceled:", sanitizeErrorText(err))
		return
	case isTimeoutError(err):
		log.Warnln("Timeout error:", sanitizeErrorText(err))
	case errors.As(err, &apiErr):
		// Telegram API errors are user-facing (bad input, etc.), no stack trace needed.
		log.Warnf("Telegram API error (code %d): %s", apiErr.Code, apiErr.Description)
	case isTransientNetworkError(err):
		// Transient network blip, no stack trace and no point resending. Will recover.
		log.Warnln("Transient network error talking to Telegram:", sanitizeErrorText(err))
		return
	case errors.Is(err, errNoStickerAvailable):
		log.Warnln("No sticker available to commit:", sanitizeErrorText(err))
		if c != nil {
			reason := noStickerAvailableReason(err)
			c.Send("No sticker was available to import. Please try another sticker link or start again. /start\n"+
				"沒有可匯入的貼圖，請換一個貼圖連結或點選 /start 重試。\n\n"+
				"Error: "+reason+"\n"+
				"If this sticker link should work, please report it here:\n"+
				"如果你認為這個貼圖連結能正確開啟，請到這裡回報：\n"+
				"https://github.com/akira02/chiaki-sticker-bot/issues", tele.NoPreview)
			cleanUserDataAndDir(c.Sender().ID)
		}
		return
	case errors.Is(err, msbimport.ErrStickerTooLarge):
		log.Warnln("Sticker too large after compression:", sanitizeErrorText(err))
		if c != nil {
			sendStickerCompressionFailed(c, err)
			cleanUserDataAndDir(c.Sender().ID)
		}
		return
	default:
		log.Error("User encountered fatal error!")
		log.Errorln("Raw error:", sanitizeErrorText(err))
		log.Errorln(string(debug.Stack()))
	}

	defer func() {
		if r := recover(); r != nil {
			log.Errorln("Recovered from onError!!", r)
		}
	}()
	if c == nil {
		return
	}
	// Record the failure into events DB so admins can audit it.
	if ud := udFromCtx(c); ud != nil && ud.command != "" {
		action := ud.command
		packID := ud.stickerData.id
		if ud.command == "import" && ud.lineData != nil {
			action = "import_" + ud.lineData.Store
			packID = ud.lineData.Id
		}
		go insertFailedEvent(c.Sender().ID, c.Sender().Username,
			strings.TrimSpace(c.Sender().FirstName+" "+c.Sender().LastName),
			action, packID, sanitizeErrorText(err))
	}
	sendFatalError(err, c)
	cleanUserDataAndDir(c.Sender().ID)
}

func noStickerAvailableReason(err error) string {
	reason := sanitizeLogText(err.Error())
	reason = strings.TrimPrefix(reason, errNoStickerAvailable.Error()+": ")
	reason = strings.TrimSuffix(reason, ": "+msbimport.ErrNoStickerFound.Error())
	if reason == "" || reason == errNoStickerAvailable.Error() {
		return "import completed without prepared sticker files"
	}
	return reason
}

// telegramAPILimiter caps our own outbound request rate comfortably below
// Telegram's global ~30 req/s bot limit. Without this, many concurrent users
// (search results, progress edits, uploads) can burst past that ceiling with
// no single caller doing anything wrong, and Telegram responds with very
// long (hours-long) 429 retry-after bans on the whole bot.
var telegramAPILimiter = rate.NewLimiter(rate.Limit(25), 25)

func telegramHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: replayableBodyTransport{next: rateLimitedTransport{next: transport}},
	}
}

type rateLimitedTransport struct {
	next http.RoundTripper
}

func (t rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// getUpdates is a long-poll heartbeat, not a flood-prone write call;
	// delaying it would just delay message reception.
	if method := req.URL.Path; !strings.HasSuffix(method, "/getUpdates") {
		if err := telegramAPILimiter.Wait(req.Context()); err != nil {
			return nil, err
		}
	}
	return t.next.RoundTrip(req)
}

type replayableBodyTransport struct {
	next http.RoundTripper
}

func (t replayableBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil || req.GetBody != nil {
		return t.next.RoundTrip(req)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}

	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	clone.ContentLength = int64(len(body))

	return t.next.RoundTrip(clone)
}

func initBot(conf ConfigTemplate) *tele.Bot {
	var poller tele.Poller
	if conf.WebhookPublicUrl != "" {
		webhookPoller = &tele.Webhook{
			Endpoint: &tele.WebhookEndpoint{
				PublicURL: conf.WebhookPublicUrl,
			},
			SecretToken: conf.WebhookSecretToken,
		}
		poller = webhookPoller
		log.WithField("url", conf.WebhookPublicUrl).Info("Webhook mode enabled.")
	} else {
		poller = &tele.LongPoller{Timeout: 10 * time.Second}
		log.Info("Long polling mode enabled.")
	}

	pref := tele.Settings{
		Token:  conf.BotToken,
		Poller: poller,
		// Use a longer timeout for file uploads. Whole-pack zip downloads can be
		// tens of MB and upload slowly from the constrained VM; 3min was too tight
		// and surfaced as "sendDocument: context deadline exceeded".
		Client:      telegramHTTPClient(),
		Synchronous: false,
		// Genrally, issues are tackled inside each state, only fatal error should be returned to framework.
		// onError will terminate current session and log to terminal.
		OnError: onError,
	}
	log.Info("Attempting to initialize bot...")
	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
	}
	return b
}

func initWorkspace(b *tele.Bot) {
	botName = b.Me.Username
	if msbconf.DataDir != "" {
		dataDir = msbconf.DataDir
	} else {
		dataDir = botName + "_data"
	}
	users = Users{data: make(map[int64]*UserData)}
	err := os.MkdirAll(dataDir, 0755)
	if err != nil {
		log.Fatal(err)
	}

	if msbconf.DbAddr != "" {
		dbName := botName + "_db"
		err = initDB(dbName)
		if err != nil {
			log.Fatalln("Error initializing database!!", err)
		}
	} else {
		log.Warn("Database not enabled because --db_addr is not set.")
	}
}

// This gocron is intended to do periodic cleanups.
func initGoCron() {
	// Delay start.
	time.Sleep(15 * time.Second)
	cronScheduler = gocron.NewScheduler(time.UTC)
	cronScheduler.WaitForScheduleAll()
	cronScheduler.Every(1).Days().Do(purgeOutdatedStorageData)
	if msbconf.DbAddr != "" {
		cronScheduler.Every(2).Days().Do(curateDatabase)
	}
	cronScheduler.StartBlocking()
}

func initLogrus() {
	log.SetFormatter(sanitizingFormatter{formatter: &log.TextFormatter{
		ForceColors:            true,
		DisableLevelTruncation: true,
	}})

	level, err := log.ParseLevel(msbconf.LogLevel)
	if err != nil {
		println("Error parsing log_level! Defaulting to DEBUG level.\n")
		log.SetLevel(log.DebugLevel)
	}
	log.SetLevel(level)

	fmt.Printf("Log level is set to: %s\n", log.GetLevel())
	log.Debug("Warning: Log level below DEBUG might print sensitive information, including passwords.")
}
