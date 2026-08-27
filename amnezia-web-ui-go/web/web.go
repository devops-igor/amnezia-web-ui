package web

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var StaticFS embed.FS

//go:embed templates/*
var TemplatesFS embed.FS

//go:embed translations/*
var TranslationsFS embed.FS

// GetStaticSubFS returns the subtree of StaticFS rooted at "static".
func GetStaticSubFS() (fs.FS, error) {
	return fs.Sub(StaticFS, "static")
}

// GetTranslationsSubFS returns the subtree of TranslationsFS rooted at "translations".
func GetTranslationsSubFS() (fs.FS, error) {
	return fs.Sub(TranslationsFS, "translations")
}

// GetTemplatesSubFS returns the subtree of TemplatesFS rooted at "templates".
func GetTemplatesSubFS() (fs.FS, error) {
	return fs.Sub(TemplatesFS, "templates")
}
