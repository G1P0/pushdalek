package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	TGBotToken string
	TGAdminID  int64
	DBPath     string

	VKToken   string
	VKOwnerID string
}

func MustLoad() Config {
	return Config{
		TGBotToken: mustEnv("TG_BOT_TOKEN"),
		TGAdminID:  mustInt64("TG_ADMIN_ID"),
		DBPath:     getenv("DB_PATH", "bot.db"),
		VKToken:    mustEnv("VK_TOKEN"),
		VKOwnerID:  mustEnv("VK_OWNER_ID"),
	}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func mustInt64(k string) int64 {
	s := mustEnv(k)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		log.Fatalf("bad %s: %v", k, err)
	}
	return v
}
