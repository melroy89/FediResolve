package resolver

import (
	"fmt"
	"net/http"
	"net/url"
)

// checkForRedirect checks if a URL redirects to another URL
// and returns the final redirect URL after following all redirects
func (r *Resolver) checkForRedirect(inputURL string) (string, error) {
	// Create a custom client that doesn't follow redirects automatically
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Follow up to 10 redirects (to prevent infinite loops)
	currentURL := inputURL
	for i := 0; i < 10; i++ {
		// Create the request
		req, err := http.NewRequest("GET", currentURL, nil)
		if err != nil {
			return "", fmt.Errorf("error creating redirect check request: %v", err)
		}

		// Set standard browser-like headers
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml")

		// Perform the request
		fmt.Printf("Checking for redirects from: %s\n", currentURL)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("error checking for redirects: %v", err)
		}

		// Check if we got a redirect (302, 301, etc.)
		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
			resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
			// Get the redirect URL from the Location header
			redirectURL := resp.Header.Get("Location")
			resp.Body.Close() // Close the response body before continuing

			if redirectURL != "" {
				fmt.Printf("Found redirect to: %s\n", redirectURL)
				
				// Handle relative URLs
				if redirectURL[0] == '/' {
					// This is a relative URL, so we need to resolve it against the current URL
					baseURL, err := url.Parse(currentURL)
					if err != nil {
						return "", fmt.Errorf("error parsing base URL: %v", err)
					}
					relativeURL, err := url.Parse(redirectURL)
					if err != nil {
						return "", fmt.Errorf("error parsing relative URL: %v", err)
					}
					resolvedURL := baseURL.ResolveReference(relativeURL)
					redirectURL = resolvedURL.String()
					fmt.Printf("Resolved relative URL to: %s\n", redirectURL)
				}
				
				// Update the current URL and continue following redirects
				currentURL = redirectURL
				continue
			}
		}

		// If we didn't get a redirect, we're done
		resp.Body.Close()
		break
	}

	// If we followed redirects and ended up at a different URL, return it
	if currentURL != inputURL {
		return currentURL, nil
	}

	// No redirect found or we ended up back at the original URL
	return "", nil
}
