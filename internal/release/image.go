package release

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxBinarySize is the largest single image any SimpleOTA plan accepts.
const MaxBinarySize = 32 << 20

// espImageMagic is the first byte of every ESP32-family app image.
const espImageMagic = 0xE9

// imageHeaderLen is the size of an app image header (esp_image_header_t).
const imageHeaderLen = 24

// espChipIDs maps the chip_id field of an app image header to the chip
// family devices report, which is ESP-IDF's IDF_TARGET. The IDs come from
// esp_chip_id_t in ESP-IDF's esp_app_format.h.
var espChipIDs = map[uint16]string{
	0x0000: "esp32",
	0x0002: "esp32s2",
	0x0005: "esp32c3",
	0x0009: "esp32s3",
	0x000C: "esp32c2",
	0x000D: "esp32c6",
	0x0010: "esp32h2",
	0x0012: "esp32p4",
	0x0014: "esp32c61",
	0x0017: "esp32c5",
	0x0019: "esp32h21",
	0x001C: "esp32h4",
}

// nonAppSuffixes are Arduino toolchain outputs that are not OTA app
// images; the server warns on the same list.
var nonAppSuffixes = []string{
	".merged.bin", ".bootloader.bin", ".partitions.bin", ".partitions_flashed.bin",
	".spiffs.bin", ".littlefs.bin", ".fatfs.bin",
}

// nonAppNames are the ESP-IDF build outputs, by file name, that are not OTA
// app images. A bootloader image has the same header as an app, so only its
// name tells them apart.
var nonAppNames = []string{"bootloader.bin", "partition-table.bin", "ota_data_initial.bin"}

const appImageHint = "\nPass the application .bin:\n  Arduino: build/<sketch>.ino.bin\n  ESP-IDF: build/<project>.bin"

// ReadBinary loads and checks a firmware image. The checks are the file
// name, the size, the first byte of the image header and, in ImageChip, its
// chip ID. They do not prove the file is an app image.
func ReadBinary(path string) ([]byte, error) {
	lower := strings.ToLower(filepath.Base(path))
	if slices.Contains(nonAppNames, lower) {
		return nil, fmt.Errorf("%s is an ESP-IDF %s, not an OTA app image%s", path, strings.TrimSuffix(lower, ".bin"), appImageHint)
	}
	for _, s := range nonAppSuffixes {
		if strings.HasSuffix(lower, s) {
			return nil, fmt.Errorf("%s is a %s file, not an OTA app image%s", path, s, appImageHint)
		}
	}
	f, err := os.Open(path) // #nosec G304 -- the user names the firmware to upload
	if err != nil {
		return nil, fmt.Errorf("opening firmware: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	if fi.Size() > MaxBinarySize {
		return nil, fmt.Errorf("%s is %.1f MiB, over the %d MiB limit for one firmware image", path, float64(fi.Size())/(1<<20), MaxBinarySize>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBinarySize+1))
	if err != nil {
		return nil, fmt.Errorf("reading firmware: %w", err)
	}
	if len(data) > MaxBinarySize {
		return nil, fmt.Errorf("%s grew past the %d MiB limit while reading", path, MaxBinarySize>>20)
	}
	if data[0] != espImageMagic {
		return nil, fmt.Errorf("%s does not look like an ESP32 app image\nIts first byte is 0x%02x, not 0xe9. Pass the .bin, not the .elf.", path, data[0])
	}
	return data, nil
}

// ImageChip returns the chip family an app image was built for, read from
// its header, or "" when the header names no chip simpleota knows.
func ImageChip(data []byte) string {
	if len(data) < imageHeaderLen || data[0] != espImageMagic {
		return ""
	}
	return espChipIDs[uint16(data[12])|uint16(data[13])<<8]
}
