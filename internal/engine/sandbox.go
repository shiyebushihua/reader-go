package engine

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// LegadoStorage provides key-value storage for a source execution context
type LegadoStorage struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewLegadoStorage() *LegadoStorage {
	return &LegadoStorage{data: make(map[string]string)}
}

func (s *LegadoStorage) Put(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
}

func (s *LegadoStorage) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// JSEnv holds context parameters passed into the Legado JS execution
type JSEnv struct {
	BaseURL string
	Result  any
	Src     string
	Key     string
	Page    int
	Book    any
	Chapter any
	Storage *LegadoStorage
	Client  *http.Client
}

// Helper: PKCS7 unpad
func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("empty data")
	}
	unpadding := int(data[length-1])
	if unpadding > length || unpadding == 0 {
		return data, nil
	}
	for i := length - unpadding; i < length; i++ {
		if data[i] != byte(unpadding) {
			return data, nil
		}
	}
	return data[:(length - unpadding)], nil
}

// Helper: PKCS7 pad
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

// Helper: AES decrypt with CBC or ECB mode fallback
func aesDecrypt(keyBytes, ivBytes, cipherBytes []byte) ([]byte, error) {
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, err
	}
	if len(cipherBytes)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ciphertext is not a multiple of the block size")
	}
	decrypted := make([]byte, len(cipherBytes))
	if len(ivBytes) >= block.BlockSize() {
		mode := cipher.NewCBCDecrypter(block, ivBytes[:block.BlockSize()])
		mode.CryptBlocks(decrypted, cipherBytes)
	} else {
		// ECB mode
		for bs, be := 0, block.BlockSize(); bs < len(cipherBytes); bs, be = bs+block.BlockSize(), be+block.BlockSize() {
			block.Decrypt(decrypted[bs:be], cipherBytes[bs:be])
		}
	}
	unpadded, _ := pkcs7Unpad(decrypted)
	return unpadded, nil
}

// Helper: AES encrypt
func aesEncrypt(keyBytes, ivBytes, plainBytes []byte) ([]byte, error) {
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, err
	}
	plainBytes = pkcs7Pad(plainBytes, block.BlockSize())
	encrypted := make([]byte, len(plainBytes))
	if len(ivBytes) >= block.BlockSize() {
		mode := cipher.NewCBCEncrypter(block, ivBytes[:block.BlockSize()])
		mode.CryptBlocks(encrypted, plainBytes)
	} else {
		for bs, be := 0, block.BlockSize(); bs < len(plainBytes); bs, be = bs+block.BlockSize(), be+block.BlockSize() {
			block.Encrypt(encrypted[bs:be], plainBytes[bs:be])
		}
	}
	return encrypted, nil
}

// Helper: decode cipher payload from base64 (std, raw, urlsafe) or hex
func decodeCipherInput(data string) []byte {
	data = strings.TrimSpace(data)
	if bs, err := base64.StdEncoding.DecodeString(data); err == nil && len(bs) > 0 {
		return bs
	}
	if bs, err := base64.RawStdEncoding.DecodeString(data); err == nil && len(bs) > 0 {
		return bs
	}
	if bs, err := base64.URLEncoding.DecodeString(data); err == nil && len(bs) > 0 {
		return bs
	}
	if bs, err := base64.RawURLEncoding.DecodeString(data); err == nil && len(bs) > 0 {
		return bs
	}
	if bs, err := hex.DecodeString(data); err == nil && len(bs) > 0 {
		return bs
	}
	return []byte(data)
}

// Helper: convert Java date format to Go layout
func javaDatePatternToGo(pattern string) string {
	pattern = strings.ReplaceAll(pattern, "yyyy", "2006")
	pattern = strings.ReplaceAll(pattern, "yy", "06")
	pattern = strings.ReplaceAll(pattern, "MM", "01")
	pattern = strings.ReplaceAll(pattern, "M", "1")
	pattern = strings.ReplaceAll(pattern, "dd", "02")
	pattern = strings.ReplaceAll(pattern, "d", "2")
	pattern = strings.ReplaceAll(pattern, "HH", "15")
	pattern = strings.ReplaceAll(pattern, "H", "15")
	pattern = strings.ReplaceAll(pattern, "hh", "03")
	pattern = strings.ReplaceAll(pattern, "h", "3")
	pattern = strings.ReplaceAll(pattern, "mm", "04")
	pattern = strings.ReplaceAll(pattern, "m", "4")
	pattern = strings.ReplaceAll(pattern, "ss", "05")
	pattern = strings.ReplaceAll(pattern, "s", "5")
	pattern = strings.ReplaceAll(pattern, "SSS", "000")
	return pattern
}

// RunLegadoJS runs a JavaScript snippet inside a protected Goja runtime
func RunLegadoJS(jsCode string, env JSEnv) (any, error) {
	jsCode = strings.TrimSpace(jsCode)
	if jsCode == "" {
		return env.Result, nil
	}

	vm := goja.New()

	// Safe timeout guard (max 5 seconds per script execution)
	time.AfterFunc(5*time.Second, func() {
		vm.Interrupt("timeout: javascript execution exceeded 5s limit")
	})

	storage := env.Storage
	if storage == nil {
		storage = NewLegadoStorage()
	}

	client := env.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	// 1. Setup 'java' host object with full Legado compatibility
	javaObj := vm.NewObject()

	// java.ajax / java.connect
	ajaxFunc := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		rawURL := call.Argument(0).String()
		if rawURL == "" || strings.HasPrefix(rawURL, "undefined") {
			return vm.ToValue("")
		}

		method := "GET"
		bodyStr := ""
		headersMap := make(map[string]string)
		charset := "utf-8"

		// Option parsing from second argument
		if len(call.Arguments) > 1 {
			arg1 := call.Argument(1)
			if optMap, ok := arg1.Export().(map[string]any); ok {
				if m, ok := optMap["method"].(string); ok && m != "" {
					method = strings.ToUpper(m)
				}
				if b, ok := optMap["body"].(string); ok {
					bodyStr = b
				}
				if c, ok := optMap["charset"].(string); ok && c != "" {
					charset = strings.ToLower(c)
				}
				if h, ok := optMap["headers"].(map[string]any); ok {
					for k, v := range h {
						headersMap[k] = fmt.Sprintf("%v", v)
					}
				}
			} else if optStr := arg1.String(); strings.HasPrefix(strings.TrimSpace(optStr), "{") {
				var optMap map[string]any
				if err := json.Unmarshal([]byte(normalizeJSONOptions(optStr)), &optMap); err == nil {
					if m, ok := optMap["method"].(string); ok && m != "" {
						method = strings.ToUpper(m)
					}
					if b, ok := optMap["body"].(string); ok {
						bodyStr = b
					}
					if c, ok := optMap["charset"].(string); ok && c != "" {
						charset = strings.ToLower(c)
					}
					if h, ok := optMap["headers"].(map[string]any); ok {
						for k, v := range h {
							headersMap[k] = fmt.Sprintf("%v", v)
						}
					}
				}
			}
		}

		// Option parsing from compound url: url,{...}
		if idx := strings.Index(rawURL, ",{"); idx != -1 {
			optJSON := rawURL[idx+1:]
			rawURL = rawURL[:idx]
			var optMap map[string]any
			if err := json.Unmarshal([]byte(normalizeJSONOptions(optJSON)), &optMap); err == nil {
				if m, ok := optMap["method"].(string); ok && m != "" {
					method = strings.ToUpper(m)
				}
				if b, ok := optMap["body"].(string); ok {
					bodyStr = b
				}
				if c, ok := optMap["charset"].(string); ok && c != "" {
					charset = strings.ToLower(c)
				}
				if h, ok := optMap["headers"].(map[string]any); ok {
					for k, v := range h {
						headersMap[k] = fmt.Sprintf("%v", v)
					}
				}
			}
		}

		// Resolve relative URL
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") && env.BaseURL != "" {
			if u, err := resolve(env.BaseURL, rawURL); err == nil {
				rawURL = u
			}
		}

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequest(method, rawURL, bodyReader)
		if err != nil {
			return vm.ToValue("")
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		for k, v := range headersMap {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			return vm.ToValue("")
		}
		defer resp.Body.Close()

		bs, _ := io.ReadAll(resp.Body)

		// Charset decode if needed
		cType := strings.ToLower(resp.Header.Get("Content-Type"))
		if charset == "gbk" || charset == "gb2312" || strings.Contains(cType, "gbk") || strings.Contains(cType, "gb2312") {
			if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(bs); err == nil {
				bs = decoded
			}
		}

		return vm.ToValue(string(bs))
	}
	_ = javaObj.Set("ajax", ajaxFunc)
	_ = javaObj.Set("connect", ajaxFunc)

	// Base64
	_ = javaObj.Set("base64Decode", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		bs := decodeCipherInput(str)
		return vm.ToValue(string(bs))
	})
	_ = javaObj.Set("base64Encode", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		encoded := base64.StdEncoding.EncodeToString([]byte(str))
		return vm.ToValue(encoded)
	})

	// Hash & Digest
	_ = javaObj.Set("md5Encode", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		h := md5.Sum([]byte(str))
		return vm.ToValue(hex.EncodeToString(h[:]))
	})
	_ = javaObj.Set("md5", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		h := md5.Sum([]byte(str))
		return vm.ToValue(hex.EncodeToString(h[:]))
	})
	_ = javaObj.Set("sha256Encode", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		h := sha256.Sum256([]byte(str))
		return vm.ToValue(hex.EncodeToString(h[:]))
	})
	_ = javaObj.Set("digest", func(call goja.FunctionCall) goja.Value {
		algo := strings.ToUpper(call.Argument(0).String())
		str := call.Argument(1).String()
		switch algo {
		case "MD5":
			h := md5.Sum([]byte(str))
			return vm.ToValue(hex.EncodeToString(h[:]))
		case "SHA-1", "SHA1":
			h := sha1.Sum([]byte(str))
			return vm.ToValue(hex.EncodeToString(h[:]))
		case "SHA-512", "SHA512":
			h := sha512.Sum512([]byte(str))
			return vm.ToValue(hex.EncodeToString(h[:]))
		default: // default SHA-256
			h := sha256.Sum256([]byte(str))
			return vm.ToValue(hex.EncodeToString(h[:]))
		}
	})

	// String manipulation
	_ = javaObj.Set("replace", func(call goja.FunctionCall) goja.Value {
		src := call.Argument(0).String()
		pat := call.Argument(1).String()
		repl := call.Argument(2).String()
		re, err := regexp.Compile(pat)
		if err != nil {
			return vm.ToValue(src)
		}
		return vm.ToValue(re.ReplaceAllString(src, repl))
	})
	_ = javaObj.Set("subString", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		start := int(call.Argument(1).ToInteger())
		runes := []rune(str)
		if start < 0 {
			start = 0
		}
		if start > len(runes) {
			return vm.ToValue("")
		}
		if len(call.Arguments) > 2 {
			end := int(call.Argument(2).ToInteger())
			if end > len(runes) {
				end = len(runes)
			}
			if end < start {
				return vm.ToValue("")
			}
			return vm.ToValue(string(runes[start:end]))
		}
		return vm.ToValue(string(runes[start:]))
	})
	_ = javaObj.Set("trim", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(strings.TrimSpace(call.Argument(0).String()))
	})

	// URI encoding/decoding with GBK support
	_ = javaObj.Set("encodeURI", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		enc := "utf-8"
		if len(call.Arguments) > 1 {
			enc = strings.ToLower(call.Argument(1).String())
		}
		if enc == "gbk" || enc == "gb2312" {
			encoder := simplifiedchinese.GBK.NewEncoder()
			gbkBytes, err := encoder.Bytes([]byte(str))
			if err == nil {
				var b strings.Builder
				for _, c := range gbkBytes {
					if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
						b.WriteByte(c)
					} else {
						fmt.Fprintf(&b, "%%%02X", c)
					}
				}
				return vm.ToValue(b.String())
			}
		}
		return vm.ToValue(url.QueryEscape(str))
	})
	_ = javaObj.Set("decodeURI", func(call goja.FunctionCall) goja.Value {
		str := call.Argument(0).String()
		unescaped, err := url.QueryUnescape(str)
		if err != nil {
			return vm.ToValue(str)
		}
		return vm.ToValue(unescaped)
	})

	// Crypto: AES / Symmetric Crypto
	_ = javaObj.Set("aesDecode", func(call goja.FunctionCall) goja.Value {
		keyStr := call.Argument(0).String()
		ivStr := call.Argument(1).String()
		cipherStr := call.Argument(2).String()
		cipherBytes := decodeCipherInput(cipherStr)
		decrypted, err := aesDecrypt([]byte(keyStr), []byte(ivStr), cipherBytes)
		if err != nil {
			return vm.ToValue(cipherStr)
		}
		return vm.ToValue(string(decrypted))
	})
	_ = javaObj.Set("aesEncode", func(call goja.FunctionCall) goja.Value {
		keyStr := call.Argument(0).String()
		ivStr := call.Argument(1).String()
		plainStr := call.Argument(2).String()
		encrypted, err := aesEncrypt([]byte(keyStr), []byte(ivStr), []byte(plainStr))
		if err != nil {
			return vm.ToValue(plainStr)
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(encrypted))
	})
	_ = javaObj.Set("createSymmetricCrypto", func(call goja.FunctionCall) goja.Value {
		// e.g. java.createSymmetricCrypto("AES/CBC/PKCS7Padding", key, iv)
		_ = call.Argument(0).String() // algo
		keyStr := call.Argument(1).String()
		ivStr := call.Argument(2).String()

		cryptoObj := vm.NewObject()
		_ = cryptoObj.Set("decryptStr", func(subCall goja.FunctionCall) goja.Value {
			input := subCall.Argument(0).String()
			cipherBytes := decodeCipherInput(input)
			decrypted, err := aesDecrypt([]byte(keyStr), []byte(ivStr), cipherBytes)
			if err != nil {
				return vm.ToValue(input)
			}
			return vm.ToValue(string(decrypted))
		})
		_ = cryptoObj.Set("encryptStr", func(subCall goja.FunctionCall) goja.Value {
			input := subCall.Argument(0).String()
			encrypted, err := aesEncrypt([]byte(keyStr), []byte(ivStr), []byte(input))
			if err != nil {
				return vm.ToValue(input)
			}
			return vm.ToValue(base64.StdEncoding.EncodeToString(encrypted))
		})
		return cryptoObj
	})

	// Storage
	_ = javaObj.Set("put", func(call goja.FunctionCall) goja.Value {
		k := call.Argument(0).String()
		v := call.Argument(1).String()
		storage.Put(k, v)
		return goja.Undefined()
	})
	_ = javaObj.Set("get", func(call goja.FunctionCall) goja.Value {
		k := call.Argument(0).String()
		return vm.ToValue(storage.Get(k))
	})
	_ = javaObj.Set("getString", func(call goja.FunctionCall) goja.Value {
		k := call.Argument(0).String()
		return vm.ToValue(storage.Get(k))
	})
	_ = javaObj.Set("putStringList", func(call goja.FunctionCall) goja.Value {
		k := call.Argument(0).String()
		arr := call.Argument(1).Export()
		bs, _ := json.Marshal(arr)
		storage.Put("list:"+k, string(bs))
		return goja.Undefined()
	})
	_ = javaObj.Set("getStringList", func(call goja.FunctionCall) goja.Value {
		k := call.Argument(0).String()
		raw := storage.Get("list:" + k)
		if raw == "" {
			return vm.ToValue([]string{})
		}
		var list []string
		_ = json.Unmarshal([]byte(raw), &list)
		return vm.ToValue(list)
	})

	// Time formatting
	_ = javaObj.Set("timeFormat", func(call goja.FunctionCall) goja.Value {
		ts := call.Argument(0).ToInteger()
		if ts == 0 {
			ts = time.Now().UnixMilli()
		}
		if ts < 1e11 {
			ts *= 1000
		}
		t := time.UnixMilli(ts)
		layout := "2006-01-02 15:04:05"
		if len(call.Arguments) > 1 {
			pat := call.Argument(1).String()
			if pat != "" {
				layout = javaDatePatternToGo(pat)
			}
		}
		return vm.ToValue(t.Format(layout))
	})

	// User agent
	uaStr := "Mozilla/5.0 (Linux; Android 14; Mobile) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"
	_ = javaObj.Set("getWebViewUA", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(uaStr)
	})
	_ = javaObj.Set("getUserAgent", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(uaStr)
	})

	_ = javaObj.Set("log", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})
	_ = javaObj.Set("toast", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})
	_ = javaObj.Set("getCookie", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		return vm.ToValue(storage.Get(k))
	})
	_ = javaObj.Set("setCookie", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		v := call.Argument(1).String()
		storage.Put(k, v)
		return goja.Undefined()
	})
	_ = vm.Set("java", javaObj)

	// 2. Setup 'cookie' object
	cookieObj := vm.NewObject()
	_ = cookieObj.Set("get", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		return vm.ToValue(storage.Get(k))
	})
	_ = cookieObj.Set("set", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		v := call.Argument(1).String()
		storage.Put(k, v)
		return goja.Undefined()
	})
	_ = cookieObj.Set("remove", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		storage.Put(k, "")
		return goja.Undefined()
	})
	_ = cookieObj.Set("getCookie", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		return vm.ToValue(storage.Get(k))
	})
	_ = cookieObj.Set("setCookie", func(call goja.FunctionCall) goja.Value {
		k := "cookie:" + call.Argument(0).String()
		v := call.Argument(1).String()
		storage.Put(k, v)
		return goja.Undefined()
	})
	_ = cookieObj.Set("allCookie", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue("")
	})
	_ = vm.Set("cookie", cookieObj)

	// 3. Inject global variables matching Legado execution context
	_ = vm.Set("result", env.Result)
	_ = vm.Set("baseUrl", env.BaseURL)
	_ = vm.Set("src", env.Src)
	_ = vm.Set("key", env.Key)
	_ = vm.Set("page", env.Page)
	if env.Book != nil {
		_ = vm.Set("book", env.Book)
	} else {
		_ = vm.Set("book", vm.NewObject())
	}
	if env.Chapter != nil {
		_ = vm.Set("chapter", env.Chapter)
	} else {
		_ = vm.Set("chapter", vm.NewObject())
	}

	// 4. Run JS script
	val, err := vm.RunString(jsCode)
	if err != nil {
		return nil, fmt.Errorf("js error: %w", err)
	}

	res := val.Export()
	if res == nil {
		return env.Result, nil
	}
	return res, nil
}

// SplitJSBlock checks for <js>...</js> or @js: patterns in a rule string
func SplitJSBlock(rule string) (mainRule string, jsCode string) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", ""
	}

	// 1. Check for <js>...</js>
	lower := strings.ToLower(rule)
	startIdx := strings.Index(lower, "<js>")
	if startIdx != -1 {
		endIdx := strings.Index(lower, "</js>")
		if endIdx != -1 && endIdx > startIdx {
			jsCode = rule[startIdx+4 : endIdx]
			mainRule = strings.TrimSpace(rule[:startIdx] + rule[endIdx+5:])
			return mainRule, strings.TrimSpace(jsCode)
		}
	}

	// 2. Check for @js:
	jsPrefixIdx := strings.Index(rule, "@js:")
	if jsPrefixIdx != -1 {
		mainRule = strings.TrimSpace(rule[:jsPrefixIdx])
		jsCode = strings.TrimSpace(rule[jsPrefixIdx+4:])
		return mainRule, jsCode
	}

	return rule, ""
}
