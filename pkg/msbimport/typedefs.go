package msbimport

import (
	"context"
	"sync"
	"sync/atomic"
)

// Line sticker types
const (
	LINE_STICKER_STATIC       = "line_s"  //普通貼圖
	LINE_STICKER_ANIMATION    = "line_a"  //動態貼圖
	LINE_STICKER_POPUP        = "line_p"  //全螢幕
	LINE_STICKER_POPUP_EFFECT = "line_f"  //特效
	LINE_EMOJI_STATIC         = "line_e"  //表情貼
	LINE_EMOJI_ANIMATION      = "line_i"  //動態表情貼
	LINE_STICKER_MESSAGE      = "line_m"  //訊息
	LINE_STICKER_NAME         = "line_n"  //隨你填
	KAKAO_EMOTICON            = "kakao_e" //KAKAOTALK普通貼圖

	LINE_SRC_PER_STICKER_TEXT = "PER_STICKER_TEXT"
	LINE_SRC_ANIMATION        = "ANIMATION"
	LINE_SRC_STATIC           = "STATIC"
	LINE_SRC_POPUP            = "POPUP"
	LINE_SRC_NAME_TEXT        = "NAME_TEXT"

	//Indicates popup effect, bot popup APNG and static PNG should be kept.
	LINE_POPUP_LAYER_BACKGROUND = "BACKGROUND"
	//Indicates popup, only popup APNG needed.
	LINE_POPUP_LAYER_FOREGROUND = "FOREGROUND"

	StoreLine  = "line"
	StoreKakao = "kakao"

	WARN_KAKAO_PREFER_SHARE_LINK = "prefer share link for kakao"
)

type LineFile struct {
	// Context for cancelling conversion work.
	Ctx context.Context
	//Waitgroup for conversion.
	Wg sync.WaitGroup
	//Convert to 100x100 emoji.
	ConvertToEmoji bool
	// path of original file
	OriginalFile string
	// path of converted filea
	ConvertedFile string
	// conversion error
	CError error
	// conversion progress/retry status
	Status *ConversionStatus
}

type ConversionStatus struct {
	mu sync.RWMutex
	// conversion retry/progress message, owned by the converter
	message string
	// slot queue notice, owned by converterQueue
	queued string
}

func NewConversionStatus() *ConversionStatus {
	return &ConversionStatus{}
}

func (s *ConversionStatus) Set(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
}

func (s *ConversionStatus) Clear() {
	s.Set("")
}

func (s *ConversionStatus) SetQueue(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queued = message
}

func (s *ConversionStatus) ClearQueue() {
	s.SetQueue("")
}

// Message prefers the queue notice: while queued that is the true state, and
// clearing it reveals the converter's own message (e.g. a compression retry)
// again instead of losing it.
func (s *ConversionStatus) Message() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.queued != "" {
		return s.queued
	}
	return s.message
}

// This is called linedata due to historical reason,
// instead, it handles "import" data, which includes kakao and line by far.
type LineData struct {
	//Waitgroup for when linedata become available.
	Wg sync.WaitGroup
	//Store type, defined in const.
	Store string
	//Store link
	Link string
	//Store links for different langs
	I18nLinks []string
	//Sticker download link, typically ZIP.
	DLink string
	//Sticker download links.
	DLinks []string
	//Sticker file paths.
	Files []*LineFile
	//Sticker category, defined in const.
	Category string
	//Sticker pack ID.
	Id string
	//Sticker title appeared in store.
	Title string
	//I18n titles for LINE sticker packs, TitleWg must be waited before using this field.
	I18nTitles []string
	//WaitGroup for I18nTitles
	TitleWg sync.WaitGroup
	//Is Animated line sticker.
	IsAnimated bool
	//Is line emoji(emoticon).
	IsEmoji bool
	Amount  int
	//Pack zip download progress in bytes. Read by the import progress UI.
	//DLBytesTotal is 0 until the size is known (or if the CDN omits it).
	DLBytesDone  atomic.Int64
	DLBytesTotal atomic.Int64
	//Post-download preparation stage, for the import progress UI.
	PrepStage atomic.Int32
	//Per-file progress within PREP_STAGE_PROCESSING (e.g. APNG sanitization).
	PrepFilesDone  atomic.Int64
	PrepFilesTotal atomic.Int64
}

// Preparation stages reported via LineData.PrepStage for the import progress UI.
const (
	PREP_STAGE_NONE        int32 = iota
	PREP_STAGE_EXTRACTING        // unpacking the downloaded zip
	PREP_STAGE_PROCESSING        // per-file post-processing (e.g. APNG fixup)
	PREP_STAGE_DOWNLOADING       // downloading stickers one by one (no single archive)
)

type LineJson struct {
	Name string
	Sku  string
	Url  string
}

// New Kakao API (https://e.kakao.com/api/items/{slug})
type KakaoItem struct {
	ThumbnailUrl string `json:"thumbnailUrl"`
	AnimatedUrl  string `json:"animatedUrl"`
	SoundUrl     string `json:"soundUrl"`
}

type KakaoContents struct {
	Items []KakaoItem `json:"items"`
}

type KakaoHero struct {
	Title string `json:"title"`
}

type KakaoJson struct {
	Hero     KakaoHero     `json:"hero"`
	Contents KakaoContents `json:"contents"`
}

type LineProductInfo struct {
	PackageID           int64     `json:"packageId"`
	OnSale              bool      `json:"onSale"`
	ValidDays           int64     `json:"validDays"`
	Title               Title     `json:"title"`
	Author              Author    `json:"author"`
	Price               []Price   `json:"price"`
	Stickers            []Sticker `json:"stickers"`
	HasAnimation        bool      `json:"hasAnimation"`
	HasSound            bool      `json:"hasSound"`
	StickerResourceType string    `json:"stickerResourceType"`
}

type Author struct {
	En     string `json:"en"`
	Es     string `json:"es"`
	In     string `json:"in"`
	Ja     string `json:"ja"`
	Ko     string `json:"ko"`
	Th     string `json:"th"`
	ZhHans string `json:"zh-Hans"`
	ZhHant string `json:"zh-Hant"`
}

type Price struct {
	Country  string  `json:"country"`
	Currency string  `json:"currency"`
	Symbol   string  `json:"symbol"`
	Price    float64 `json:"price"`
}

type Sticker struct {
	ID     int64 `json:"id"`
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
}

type Title struct {
	En     string `json:"en"`
	Es     string `json:"es"`
	In     string `json:"in"`
	Ja     string `json:"ja"`
	Ko     string `json:"ko"`
	Th     string `json:"th"`
	ZhHans string `json:"zh-Hans"`
	ZhHant string `json:"zh-Hant"`
}
