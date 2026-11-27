package volcengine

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/asr"
)

// Corpus selects native dictionary resources and optional context. When a
// table name and ID are both supplied, the provider gives the ID precedence.
type Corpus struct {
	BoostingTableName     string
	BoostingTableID       string
	CorrectTableName      string
	CorrectTableID        string
	RegexCorrectTableName string
	RegexCorrectTableID   string
	// Context is sent in caller-supplied order, with asr.Config.Hints in
	// the same serialized context object. The service applies token limits.
	Context []ContextItem
	// CityName is optional location context for POI assistance.
	CityName string
}

// ContextItem supplies historical dialogue, scene text or one image URL.
// Speaker may be user or bot, or empty for scene/context text. An image URL
// requires second-pass recognition and Seed-ASR 2.0; image validation and
// hosting are the caller's responsibility. The SDK never fetches the URL.
type ContextItem struct {
	Speaker  string `json:"speaker,omitempty"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// SensitiveWordsFilter configures provider-side transcript replacement.
// A nil pointer leaves the service default unchanged.
type SensitiveWordsFilter struct {
	SystemReservedFilter *bool    `json:"system_reserved_filter,omitempty"`
	FilterWithEmpty      []string `json:"filter_with_empty,omitempty"`
	FilterWithSigned     []string `json:"filter_with_signed,omitempty"`
}

type contextHotword struct {
	Word string `json:"word"`
}

type contextLocation struct {
	CityName string `json:"city_name"`
}

type contextPayload struct {
	Hotwords    []contextHotword `json:"hotwords,omitempty"`
	ContextType string           `json:"context_type,omitempty"`
	ContextData []ContextItem    `json:"context_data,omitempty"`
	Location    *contextLocation `json:"loc_info,omitempty"`
}

func applyEndpointOptions(o Options, endWindow time.Duration, secondPass bool, req map[string]any) error {
	if v := o.VADSegmentDuration; v != nil {
		if *v <= 0 || *v%time.Millisecond != 0 {
			return argumentError("VADSegmentDuration must be positive whole milliseconds")
		}
		if endWindow != 0 {
			return argumentError("VADSegmentDuration conflicts with EndWindowSize")
		}
		req["vad_segment_duration"] = int64(*v / time.Millisecond)
	}
	if v := o.ForceToSpeechTime; v != nil {
		if *v < 0 || *v%time.Millisecond != 0 {
			return argumentError("ForceToSpeechTime must be nonnegative whole milliseconds")
		}
		req["force_to_speech_time"] = int64(*v / time.Millisecond)
	}
	for key, v := range map[string]*bool{
		"enable_poi_fc": o.EnablePOIFC, "enable_music_fc": o.EnableMusicFC,
	} {
		if v == nil {
			continue
		}
		if *v && !secondPass {
			return argumentError("POI and music assistance require EnableNonStream")
		}
		req[key] = *v
	}
	if filter := o.SensitiveWords; filter != nil {
		for _, list := range [][]string{filter.FilterWithEmpty, filter.FilterWithSigned} {
			for _, word := range list {
				if !validContextText(word) {
					return argumentError("sensitive word must be nonempty valid UTF-8")
				}
			}
		}
		data, err := json.Marshal(filter)
		if err != nil {
			return err
		}
		req["sensitive_words_filter"] = string(data)
	}
	return nil
}

func validContextText(text string) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != ""
}

func applyCorpus(cfg asr.Config, req map[string]any) error {
	o, err := nativeOptions(cfg.Options)
	if err != nil {
		return err
	}
	secondPass, _ := req["enable_nonstream"].(bool)
	wire := make(map[string]any)
	var ctx contextPayload
	for _, hint := range cfg.Hints {
		if !validContextText(hint) {
			return argumentError("ASR hint must be nonempty valid UTF-8")
		}
		ctx.Hotwords = append(ctx.Hotwords, contextHotword{Word: strings.TrimSpace(hint)})
	}
	if c := o.Corpus; c != nil {
		for key, value := range map[string]string{
			"boosting_table_name": c.BoostingTableName, "boosting_table_id": c.BoostingTableID,
			"correct_table_name": c.CorrectTableName, "correct_table_id": c.CorrectTableID,
			"regex_correct_table_name": c.RegexCorrectTableName, "regex_correct_table_id": c.RegexCorrectTableID,
		} {
			if value != "" {
				if !validContextText(value) || strings.TrimSpace(value) != value {
					return argumentError("invalid corpus table name or ID")
				}
				wire[key] = value
			}
		}
		if c.CityName != "" {
			if !validContextText(c.CityName) {
				return argumentError("invalid corpus city name")
			}
			ctx.Location = &contextLocation{CityName: c.CityName}
		}
		images := 0
		for _, item := range c.Context {
			switch item.Speaker {
			case "", "user", "bot":
			default:
				return argumentError("context speaker must be user or bot")
			}
			if item.Text != "" && !validContextText(item.Text) {
				return argumentError("invalid context text")
			}
			if item.ImageURL != "" {
				images++
				u, err := url.Parse(item.ImageURL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
					return argumentError("context image requires an HTTP(S) URL")
				}
				if !secondPass || images > 1 {
					return argumentError("context images require EnableNonStream and at most one image")
				}
			}
			if item.Text == "" && item.ImageURL == "" {
				return argumentError("empty context item")
			}
		}
		if len(c.Context) != 0 {
			ctx.ContextType = "dialog_ctx"
			ctx.ContextData = c.Context
		}
	}
	if len(ctx.Hotwords) != 0 || len(ctx.ContextData) != 0 || ctx.Location != nil {
		data, err := json.Marshal(ctx)
		if err != nil {
			return err
		}
		wire["context"] = string(data)
	}
	if len(wire) != 0 {
		req["corpus"] = wire
	}
	return nil
}
