package update

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BundleVersion reads CFBundleShortVersionString from an app bundle's
// Contents/Info.plist. Release builds write the tag without its "v" prefix
// there, which lets the updater reject an artifact built for another tag.
func BundleVersion(bundle string) (string, error) {
	path := filepath.Join(bundle, "Contents", "Info.plist")
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read bundle version: %w", err)
	}
	defer f.Close()
	v, err := plistString(f, "CFBundleShortVersionString")
	if err != nil {
		return "", fmt.Errorf("read bundle version: %w", err)
	}
	if v == "" {
		return "", fmt.Errorf("bundle has no CFBundleShortVersionString")
	}
	return v, nil
}

// plistString returns the string value of key in an XML property list. Info
// plists from Wails are XML; a binary plist is rejected rather than guessed at.
func plistString(r io.Reader, want string) (string, error) {
	dec := xml.NewDecoder(r)
	var key string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var k string
			if err := dec.DecodeElement(&k, &start); err != nil {
				return "", err
			}
			key = k
		case "string":
			var v string
			if err := dec.DecodeElement(&v, &start); err != nil {
				return "", err
			}
			if key == want {
				return v, nil
			}
			key = ""
		}
	}
}
