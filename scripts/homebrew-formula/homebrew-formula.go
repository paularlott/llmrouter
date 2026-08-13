package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"github.com/paularlott/llmrouter/build"
)

// ──────────────────────────────────────────────────────────────────────────
// Templates
// ──────────────────────────────────────────────────────────────────────────

// formulaTemplate — single binary. macOS zip contains .app, Linux zip contains
// bare binary. The formula handles both.
const formulaTemplate = `class Llmrouter < Formula
	desc "{{ .Desc }}"
	homepage "{{ .Homepage }}"
	license "MIT"
	version "{{ .Version }}"

	on_mac do
		on_arm do
			url "{{ .Repo }}/releases/download/v#{version}/llmrouter-darwin-arm64.zip"
			sha256 "{{ .Checksum.DarwinArm64 }}"
		end
		on_intel do
			url "{{ .Repo }}/releases/download/v#{version}/llmrouter-darwin-amd64.zip"
			sha256 "{{ .Checksum.DarwinAmd64 }}"
		end
	end

	on_linux do
		on_arm do
			url "{{ .Repo }}/releases/download/v#{version}/llmrouter-linux-arm64.zip"
			sha256 "{{ .Checksum.LinuxArm64 }}"
		end
		on_intel do
			url "{{ .Repo }}/releases/download/v#{version}/llmrouter-linux-amd64.zip"
			sha256 "{{ .Checksum.LinuxAmd64 }}"
		end
	end

	def install
		if OS.mac?
			# macOS zip contains "LLM Router.app" — install to libexec, symlink binary
			libexec.install Dir["LLM Router.app"]
			bin.install_symlink libexec/"LLM Router.app/Contents/MacOS/llmrouter"
		else
			bin.install "llmrouter"
		end
	end

	def caveats
		on_macos do
			<<~EOS
				For the full desktop GUI experience, install the cask instead:
				  brew install --cask paularlott/tap/llmrouter
			EOS
		end
	end
end
`

// macCaskTemplate — macOS .app bundle, auto-installs to /Applications.
// The postflight hook also symlinks the binary into PATH so `llmrouter`
// works from the terminal without needing the formula.
const macCaskTemplate = `cask "llmrouter" do
	version "{{ .Version }}"

	on_arm do
		sha256 "{{ .Checksum.DarwinArm64 }}"
		url "{{ .Repo }}/releases/download/v#{version}/llmrouter-darwin-arm64.zip"
	end
	on_intel do
		sha256 "{{ .Checksum.DarwinAmd64 }}"
		url "{{ .Repo }}/releases/download/v#{version}/llmrouter-darwin-amd64.zip"
	end

	name "LLM Router"
	desc "{{ .Desc }}"
	homepage "{{ .Homepage }}"
	license "MIT"

	app "LLM Router.app"

	# Also make the binary available on PATH so the llmrouter command works
	# from the terminal without separately installing the formula.
	postflight do
		ln_sf("/Applications/LLM Router.app/Contents/MacOS/llmrouter", "#{HOMEBREW_PREFIX}/bin/llmrouter")
	end

	uninstall_postflight do
		rm_f "#{HOMEBREW_PREFIX}/bin/llmrouter"
	end

	zap trash: [
		"~/Library/Caches/com.paularlott.llmrouter",
		"~/Library/Preferences/com.paularlott.llmrouter.plist",
		"~/Library/Saved Application State/com.paularlott.llmrouter.savedState",
	]
end
`

// ──────────────────────────────────────────────────────────────────────────
// Data
// ──────────────────────────────────────────────────────────────────────────

type checksums struct {
	DarwinArm64 string
	DarwinAmd64 string
	LinuxArm64  string
	LinuxAmd64  string
}

type templateData struct {
	Version  string
	Desc     string
	Homepage string
	Repo     string
	Checksum checksums
}

func checksumFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func render(tmplStr string, data templateData, path string) error {
	tmpl, err := template.New("file").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	fmt.Println("wrote", path)
	return nil
}

func main() {
	outDir := flag.String("out", ".", "Root directory of the homebrew tap")
	flag.Parse()

	data := templateData{
		Version:  build.Version,
		Desc:     "A unified gateway that aggregates multiple LLM providers behind a single endpoint",
		Homepage: "https://github.com/paularlott/llmrouter",
		Repo:     "https://github.com/paularlott/llmrouter",
		Checksum: checksums{
			DarwinArm64: checksumFile("dist/llmrouter-darwin-arm64.zip"),
			DarwinAmd64: checksumFile("dist/llmrouter-darwin-amd64.zip"),
			LinuxArm64:  checksumFile("dist/llmrouter-linux-arm64.zip"),
			LinuxAmd64:  checksumFile("dist/llmrouter-linux-amd64.zip"),
		},
	}

	if err := render(formulaTemplate, data, filepath.Join(*outDir, "Formula", "llmrouter.rb")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := render(macCaskTemplate, data, filepath.Join(*outDir, "Casks", "llmrouter.rb")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
