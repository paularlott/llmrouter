package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/paularlott/llmrouter/build"
)

const formulaTemplate = `class Llmrouter < Formula
	desc "A unified gateway that aggregates multiple LLM providers behind a single endpoint"
	homepage "https://github.com/paularlott/llmrouter"
	license "MIT"
	version "{{ .Version }}"
	if OS.mac?
		if Hardware::CPU.arm?
			url "https://github.com/paularlott/llmrouter/releases/download/v#{version}/llmrouter-darwin-arm64.zip"
			sha256 "{{ .Checksum.DarwinArm64 }}"
		else
			url "https://github.com/paularlott/llmrouter/releases/download/v#{version}/llmrouter-darwin-amd64.zip"
			sha256 "{{ .Checksum.DarwinAmd64 }}"
		end
	elsif OS.linux?
		if Hardware::CPU.arm?
			url "https://github.com/paularlott/llmrouter/releases/download/v#{version}/llmrouter-linux-arm64.zip"
			sha256 "{{ .Checksum.LinuxArm64 }}"
		else
			url "https://github.com/paularlott/llmrouter/releases/download/v#{version}/llmrouter-linux-amd64.zip"
			sha256 "{{ .Checksum.LinuxAmd64 }}"
		end
	end

	def install
		bin.install "llmrouter"
	end
end
`

func main() {
	data := struct {
		Version  string
		Checksum struct {
			DarwinArm64 string
			DarwinAmd64 string
			LinuxArm64  string
			LinuxAmd64  string
		}
	}{
		Checksum: struct {
			DarwinArm64 string
			DarwinAmd64 string
			LinuxArm64  string
			LinuxAmd64  string
		}{
			DarwinArm64: "",
			DarwinAmd64: "",
			LinuxArm64:  "",
			LinuxAmd64:  "",
		},
		Version: build.Version,
	}

	// Calculate the SHA256 checksums
	files := map[string]*string{
		"dist/llmrouter-darwin-amd64.zip": &data.Checksum.DarwinAmd64,
		"dist/llmrouter-darwin-arm64.zip": &data.Checksum.DarwinArm64,
		"dist/llmrouter-linux-amd64.zip":  &data.Checksum.LinuxAmd64,
		"dist/llmrouter-linux-arm64.zip":  &data.Checksum.LinuxArm64,
	}

	for file, checksum := range files {
		f, err := os.Open(file)
		if err != nil {
			fmt.Printf("Error opening file %s: %v\n", file, err)
			return
		}

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			fmt.Printf("Error calculating checksum for file %s: %v\n", file, err)
			f.Close()
			return
		}

		*checksum = fmt.Sprintf("%x", h.Sum(nil))

		f.Close()
	}

	tmpl, err := template.New("formula").Parse(formulaTemplate)
	if err != nil {
		fmt.Println("Error creating template:", err)
		return
	}

	err = tmpl.Execute(os.Stdout, data)
	if err != nil {
		fmt.Println("Error executing template:", err)
	}
}
