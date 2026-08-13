package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Port           int    `mapstructure:"PORT"`
	PSQLURL        string `mapstructure:"PSQL_URL"`
	MediaPath      string `mapstructure:"MEDIA_PATH"`
	StreamsPath    string `mapstructure:"STREAMS_PATH"`
	SegmentSeconds int    `mapstructure:"SEGMENT_SECONDS"`
	WindowSegments int    `mapstructure:"WINDOW_SEGMENTS"`
	Encoder        string `mapstructure:"ENCODER"`
	VideoWidth     int    `mapstructure:"VIDEO_WIDTH"`
	VideoHeight    int    `mapstructure:"VIDEO_HEIGHT"`
	VideoBitrateK  int    `mapstructure:"VIDEO_BITRATE_K"`
	FFmpegPath     string `mapstructure:"FFMPEG_PATH"`
	FFprobePath    string `mapstructure:"FFPROBE_PATH"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("PORT", 8080)
	v.SetDefault("PSQL_URL", "")
	v.SetDefault("MEDIA_PATH", "/media")
	v.SetDefault("STREAMS_PATH", "/streams")
	v.SetDefault("SEGMENT_SECONDS", 4)
	v.SetDefault("WINDOW_SEGMENTS", 30)
	v.SetDefault("ENCODER", "nvenc")
	v.SetDefault("VIDEO_WIDTH", 1920)
	v.SetDefault("VIDEO_HEIGHT", 1080)
	v.SetDefault("VIDEO_BITRATE_K", 5000)
	v.SetDefault("FFMPEG_PATH", "ffmpeg")
	v.SetDefault("FFPROBE_PATH", "ffprobe")

	// Optional .env in the working directory. Absent is normal — the Docker
	// image passes real environment variables instead. Viper ranks env above
	// config file, so an exported var still overrides the file.
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}
