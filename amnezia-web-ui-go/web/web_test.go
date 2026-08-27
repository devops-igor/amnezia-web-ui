package web

import (
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
	}
}

func TestEmbeddedStaticAndTemplates(t *testing.T) {
	staticFS, err := GetStaticSubFS()
	if err != nil {
		t.Fatalf("GetStaticSubFS failed: %v", err)
	}

	if _, err := fs.Stat(staticFS, "css"); err != nil {
		t.Errorf("static/css directory not found in embed: %v", err)
	}

	templatesFS, err := GetTemplatesSubFS()
	if err != nil {
		t.Fatalf("GetTemplatesSubFS failed: %v", err)
	}

	if _, err := fs.Stat(templatesFS, "base.html"); err != nil {
		t.Errorf("templates/base.html not found in embed: %v", err)
	}
}
