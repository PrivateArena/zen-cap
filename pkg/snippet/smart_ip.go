package snippet

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	cachedIPAddress string
	cachedIPErr     error
	cachedIPFetched bool
)

// TriggerIPFetch requests the IP dynamically if not already loaded.
func (s *SmartState) TriggerIPFetch(onDone func()) {
	if s.ipLoading || s.ipFetched {
		return
	}
	s.ipLoading = true
	go func() {
		defer func() {
			s.ipLoading = false
			s.ipFetched = true
			cachedIPAddress = s.ipAddress
			cachedIPErr = s.ipErr
			cachedIPFetched = true
			if onDone != nil {
				onDone()
			}
		}()

		// Plain text endpoints are immune to JSON parsing issues (like Cloudflare HTML block pages)
		endpoints := []string{
			"https://api.ipify.org",
			"https://icanhazip.com",
			"https://ifconfig.me/ip",
		}

		client := &http.Client{Timeout: 3 * time.Second}
		var lastErr error

		for _, url := range endpoints {
			resp, err := client.Get(url)
			if err != nil {
				lastErr = err
				log.Printf("[SmartSnippet] Warning: Lookup on %s failed: %v", url, err)
				continue
			}

			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				log.Printf("[SmartSnippet] Warning: Failed to read response from %s: %v", url, readErr)
				continue
			}

			ip := strings.TrimSpace(string(bodyBytes))
			if ip == "" {
				lastErr = fmt.Errorf("empty response from %s", url)
				continue
			}

			// If it looks like HTML, it's a block page or error portal
			if strings.HasPrefix(ip, "<") {
				lastErr = fmt.Errorf("received HTML instead of IP from %s", url)
				log.Printf("[SmartSnippet] Warning: %s returned HTML", url)
				continue
			}

			s.ipAddress = ip
			s.ipErr = nil
			return
		}

		s.ipErr = fmt.Errorf("all IP lookups failed (last: %v)", lastErr)
	}()
}
