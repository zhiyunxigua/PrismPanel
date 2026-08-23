package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	DefaultTimezone = "Asia/Shanghai"
	minimumInterval = 60
	maximumInterval = 365 * 24 * 60 * 60
)

type Config struct {
	RunAt           string `json:"run_at,omitempty"`
	Time            string `json:"time,omitempty"`
	Weekdays        []int  `json:"weekdays,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
}

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func NormalizeTimezone(value string) (string, *time.Location, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		name = DefaultTimezone
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return "", nil, &ValidationError{Message: "时区无效"}
	}
	return name, location, nil
}

func NormalizeConfig(kind string, raw json.RawMessage) (json.RawMessage, error) {
	config, err := parseConfig(kind, raw)
	if err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func FirstRun(
	kind string, raw json.RawMessage, timezone string, now time.Time,
) (*time.Time, error) {
	config, err := parseConfig(kind, raw)
	if err != nil {
		return nil, err
	}
	_, location, err := NormalizeTimezone(timezone)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "once":
		runAt, err := time.Parse(time.RFC3339, config.RunAt)
		if err != nil || !runAt.After(now) {
			return nil, &ValidationError{Message: "单次任务的执行时间必须晚于当前时间"}
		}
		value := runAt.UTC()
		return &value, nil
	case "daily":
		value := nextDaily(config, location, now)
		return &value, nil
	case "weekly":
		value := nextWeekly(config, location, now)
		return &value, nil
	case "interval":
		value := now.Add(time.Duration(config.IntervalSeconds) * time.Second).UTC()
		return &value, nil
	default:
		return nil, &ValidationError{Message: "不支持的定时规则"}
	}
}

func NextAfter(
	kind string,
	raw json.RawMessage,
	timezone string,
	scheduledFor time.Time,
	now time.Time,
) (*time.Time, error) {
	config, err := parseConfig(kind, raw)
	if err != nil {
		return nil, err
	}
	_, location, err := NormalizeTimezone(timezone)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "once":
		return nil, nil
	case "daily":
		value := nextDaily(config, location, now)
		return &value, nil
	case "weekly":
		value := nextWeekly(config, location, now)
		return &value, nil
	case "interval":
		interval := time.Duration(config.IntervalSeconds) * time.Second
		steps := now.Sub(scheduledFor)/interval + 1
		if steps < 1 {
			steps = 1
		}
		value := scheduledFor.Add(steps * interval).UTC()
		return &value, nil
	default:
		return nil, &ValidationError{Message: "不支持的定时规则"}
	}
}

func parseConfig(kind string, raw json.RawMessage) (Config, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, &ValidationError{Message: "定时规则格式无效"}
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Config{}, &ValidationError{Message: "定时规则格式无效"}
	}
	switch kind {
	case "once":
		if _, err := time.Parse(time.RFC3339, config.RunAt); err != nil {
			return Config{}, &ValidationError{Message: "单次任务执行时间无效"}
		}
		config.Time, config.Weekdays, config.IntervalSeconds = "", nil, 0
	case "daily":
		if _, _, _, err := parseClock(config.Time); err != nil {
			return Config{}, err
		}
		config.RunAt, config.Weekdays, config.IntervalSeconds = "", nil, 0
	case "weekly":
		if _, _, _, err := parseClock(config.Time); err != nil {
			return Config{}, err
		}
		if len(config.Weekdays) == 0 {
			return Config{}, &ValidationError{Message: "每周任务至少选择一天"}
		}
		seen := make(map[int]struct{}, len(config.Weekdays))
		for _, weekday := range config.Weekdays {
			if weekday < 1 || weekday > 7 {
				return Config{}, &ValidationError{Message: "每周任务的星期值无效"}
			}
			seen[weekday] = struct{}{}
		}
		config.Weekdays = config.Weekdays[:0]
		for weekday := range seen {
			config.Weekdays = append(config.Weekdays, weekday)
		}
		sort.Ints(config.Weekdays)
		config.RunAt, config.IntervalSeconds = "", 0
	case "interval":
		if config.IntervalSeconds < minimumInterval || config.IntervalSeconds > maximumInterval {
			return Config{}, &ValidationError{Message: "固定间隔必须在 60 秒到 365 天之间"}
		}
		config.RunAt, config.Time, config.Weekdays = "", "", nil
	default:
		return Config{}, &ValidationError{Message: "不支持的定时规则"}
	}
	return config, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	return fmt.Errorf("unexpected JSON content")
}

func parseClock(value string) (int, int, int, error) {
	text := strings.TrimSpace(value)
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.ParseInLocation(layout, text, time.UTC)
		if err == nil {
			return parsed.Hour(), parsed.Minute(), parsed.Second(), nil
		}
	}
	return 0, 0, 0, &ValidationError{Message: "执行时间必须使用 HH:mm 或 HH:mm:ss 格式"}
}

func nextDaily(config Config, location *time.Location, after time.Time) time.Time {
	hour, minute, second, _ := parseClock(config.Time)
	local := after.In(location)
	candidate := time.Date(
		local.Year(), local.Month(), local.Day(), hour, minute, second, 0, location,
	)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate.UTC()
}

func nextWeekly(config Config, location *time.Location, after time.Time) time.Time {
	hour, minute, second, _ := parseClock(config.Time)
	local := after.In(location)
	selected := make(map[int]struct{}, len(config.Weekdays))
	for _, weekday := range config.Weekdays {
		selected[weekday] = struct{}{}
	}
	for offset := 0; offset <= 7; offset++ {
		date := local.AddDate(0, 0, offset)
		weekday := int(date.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if _, exists := selected[weekday]; !exists {
			continue
		}
		candidate := time.Date(
			date.Year(), date.Month(), date.Day(), hour, minute, second, 0, location,
		)
		if candidate.After(local) {
			return candidate.UTC()
		}
	}
	return local.AddDate(0, 0, 7).UTC()
}
