package extension

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- the device protocols extensions speak (Tuya, miIO) are defined with MD5
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- offered to extensions because device protocols need it; nothing here relies on it for safety
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"

	"github.com/dop251/goja"
)

func newHash(vm *goja.Runtime, algorithm string) func() hash.Hash {
	switch algorithm {
	case "md5":
		return md5.New
	case "sha1":
		return sha1.New
	case "sha256":
		return sha256.New
	case "sha512":
		return sha512.New
	}
	panic(goError(vm, fmt.Errorf("unknown hash %q", algorithm)))
}

type aesOptions struct {
	Mode    string `json:"mode"`
	Key     string `json:"key"`
	IV      string `json:"iv"`
	AAD     string `json:"aad"`
	Data    string `json:"data"`
	Padding bool   `json:"padding"`
}

func pad(data []byte) []byte {
	n := aes.BlockSize - len(data)%aes.BlockSize
	for range n {
		data = append(data, byte(n))
	}
	return data
}

func unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("nothing to unpad")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > aes.BlockSize || n > len(data) {
		return nil, errors.New("bad padding: wrong key, or not padded")
	}
	return data[:len(data)-n], nil
}

func runAES(vm *goja.Runtime, value goja.Value, encrypt bool) string {
	text, err := stringify(vm, value)
	throw(vm, err)
	var o aesOptions
	throw(vm, json.Unmarshal([]byte(text), &o))
	block, err := aes.NewCipher(unhex(vm, o.Key))
	throw(vm, err)
	data, iv := unhex(vm, o.Data), unhex(vm, o.IV)
	if o.Mode == "gcm" {
		aead, err := cipher.NewGCMWithNonceSize(block, max(len(iv), 1))
		throw(vm, err)
		if encrypt {
			return hex.EncodeToString(aead.Seal(nil, iv, data, unhex(vm, o.AAD)))
		}
		plain, err := aead.Open(nil, iv, data, unhex(vm, o.AAD))
		throw(vm, err)
		return hex.EncodeToString(plain)
	}
	if encrypt && o.Padding {
		data = pad(data)
	}
	if len(data)%aes.BlockSize != 0 {
		panic(goError(vm, errors.New("data isn't a whole number of blocks; set padding to true when encrypting")))
	}
	out := make([]byte, len(data))
	switch {
	case o.Mode == "ecb":
		for start := 0; start < len(data); start += aes.BlockSize {
			if encrypt {
				block.Encrypt(out[start:], data[start:])
			} else {
				block.Decrypt(out[start:], data[start:])
			}
		}
	case o.Mode == "cbc" && len(iv) == aes.BlockSize:
		if encrypt {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		} else {
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
		}
	default:
		panic(goError(vm, fmt.Errorf(`mode must be "ecb", "cbc" with a 16-byte iv, or "gcm", not %q`, o.Mode)))
	}
	if !encrypt && o.Padding {
		out, err = unpad(out)
		throw(vm, err)
	}
	return hex.EncodeToString(out)
}

// cryptoObject is ctx.crypto: what device protocols tend to need, on hex strings.
func cryptoObject(vm *goja.Runtime) map[string]any {
	return map[string]any{
		"hash": func(algorithm, data string) string {
			h := newHash(vm, algorithm)()
			h.Write(unhex(vm, data))
			return hex.EncodeToString(h.Sum(nil))
		},
		"hmac": func(algorithm, key, data string) string {
			h := hmac.New(newHash(vm, algorithm), unhex(vm, key))
			h.Write(unhex(vm, data))
			return hex.EncodeToString(h.Sum(nil))
		},
		"encrypt": func(options goja.Value) string { return runAES(vm, options, true) },
		"decrypt": func(options goja.Value) string { return runAES(vm, options, false) },
		"random": func(size int) string {
			if size < 1 || size > 4096 {
				panic(goError(vm, errors.New("random takes 1 to 4096 bytes")))
			}
			data := make([]byte, size)
			_, err := rand.Read(data)
			throw(vm, err)
			return hex.EncodeToString(data)
		},
		"hex":  func(text string) string { return hex.EncodeToString([]byte(text)) },
		"text": func(data string) string { return string(unhex(vm, data)) },
		"fromBase64": func(text string) string {
			data, err := base64.StdEncoding.DecodeString(text)
			throw(vm, err)
			return hex.EncodeToString(data)
		},
		"toBase64": func(data string) string { return base64.StdEncoding.EncodeToString(unhex(vm, data)) },
	}
}
