package web

import (
	"encoding/json"
	"io/fs"
	"testing"
)

func TestEmbeddedTranslations(t *testing.T) {
	transFS, err := GetTranslationsSubFS()
	if err != nil {
		t.Fatalf("GetTranslationsSubFS failed: %v", err)
	}

	requiredTranslations := []string{"en.json", "fa.json", "fr.json", "ru.json", "zh.json"}
	for _, langFile := range requiredTranslations {
		data, err := fs.ReadFile(transFS, langFile)
		if err != nil {
			t.Errorf("failed to read embedded translation %s: %v", langFile, err)
		}
		if len(data) == 0 {
			t.Errorf("embedded translation %s is empty", langFile)
		}

		var parsed map[string]string
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("embedded translation %s is not valid JSON: %v", langFile, err)
		}
		if len(parsed) == 0 {
			t.Errorf("embedded translation %s parsed to empty map", langFile)
		}
	}
}

func TestEmbeddedStaticAndTemplates(t *testing.T) {
	staticFS, err := GetStaticSubFS()
	if err != nil {
		t.Fatalf("GetStaticSubFS failed: %v", err)
	}

	staticDirs := []string{"css", "js"}
	for _, dir := range staticDirs {
		if _, err := fs.Stat(staticFS, dir); err != nil {
			t.Errorf("static/%s directory not found in embed: %v", dir, err)
		}
	}

	staticFiles := []string{
		"css/style.css",
		"js/qrcode.min.js",
		"favicon.svg",
	}
	for _, file := range staticFiles {
		if data, err := fs.ReadFile(staticFS, file); err != nil || len(data) == 0 {
			t.Errorf("static/%s missing or empty: %v", file, err)
		}
	}

	templatesFS, err := GetTemplatesSubFS()
	if err != nil {
		t.Fatalf("GetTemplatesSubFS failed: %v", err)
	}

	requiredTemplates := []string{
		"base.html",
		"index.html",
		"server.html",
		"users.html",
		"my_connections.html",
		"settings.html",
		"login.html",
		"setup.html",
		"change_password.html",
		"leaderboard.html",
		"user_share.html",
	}

	for _, tmpl := range requiredTemplates {
		data, err := fs.ReadFile(templatesFS, tmpl)
		if err != nil {
			t.Errorf("template %s not found in embed: %v", tmpl, err)
		}
		if len(data) == 0 {
			t.Errorf("template %s is empty", tmpl)
		}
	}
}
