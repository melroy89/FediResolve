package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Resolver handles the resolution of Fediverse URLs and handles
type Resolver struct {
	client *http.Client
}

// NewResolver creates a new Resolver instance
func NewResolver() *Resolver {
	return &Resolver{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ResolveInput is a convenience function that creates a new resolver and resolves the input
func ResolveInput(input string) (string, error) {
	r := NewResolver()
	return r.Resolve(input)
}

// Resolve takes a URL or handle and resolves it to a formatted result
func (r *Resolver) Resolve(input string) (string, error) {
	// Always prepend https:// if missing and not a handle
	inputNorm := input
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") && !strings.Contains(input, "@") {
		inputNorm = "https://" + input
	}

	parsedURL, err := url.Parse(inputNorm)
	if err == nil && parsedURL.Host != "" && (parsedURL.Path == "" || parsedURL.Path == "/") && parsedURL.RawQuery == "" && parsedURL.Fragment == "" {
		// Looks like a root domain (with or without scheme), fetch nodeinfo
		raw, nodeinfo, _, err := r.ResolveObjectOrNodeInfo(parsedURL.String())
		if err != nil {
			return "", err
		}
		formatted, ferr := FormatHelperResult(raw, nodeinfo)
		if ferr != nil {
			return string(raw), nil
		}
		return formatted, nil
	}

	// If not a root domain, proceed with other checks
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		fmt.Println("Detected URL, attempting direct resolution")
		return r.resolveURL(input)
	}

	if strings.Contains(input, "@") {
		if !strings.Contains(input, "/") && !strings.Contains(input, ":") {
			if strings.HasPrefix(input, "@") {
				if strings.Count(input, "@") == 2 {
					fmt.Println("Detected Fediverse handle, using WebFinger resolution")
					return r.resolveHandle(input)
				}
			} else {
				if strings.Count(input, "@") == 1 {
					fmt.Println("Detected Fediverse handle, using WebFinger resolution")
					return r.resolveHandle(input)
				}
			}
		}
	}

	fmt.Println("Input format unclear, attempting URL resolution")
	return r.resolveURL(input)
}

// WebFingerResponse represents the structure of a WebFinger response
type WebFingerResponse struct {
	Subject string `json:"subject"`
	Links   []struct {
		Rel  string `json:"rel"`
		Type string `json:"type"`
		Href string `json:"href"`
	} `json:"links"`
}

// resolveHandle resolves a Fediverse handle using WebFinger
func (r *Resolver) resolveHandle(handle string) (string, error) {
	// Remove @ prefix if present
	if handle[0] == '@' {
		handle = handle[1:]
	}

	// Split handle into username and domain
	parts := strings.Split(handle, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid handle format: %s", handle)
	}

	username, domain := parts[0], parts[1]

	// Construct WebFinger URL with proper URL encoding
	resource := fmt.Sprintf("acct:%s@%s", username, domain)
	webfingerURL := fmt.Sprintf("https://%s/.well-known/webfinger?resource=%s",
		domain, url.QueryEscape(resource))

	fmt.Printf("Fetching WebFinger data from: %s\n", webfingerURL)

	// Create request for WebFinger data
	req, err := http.NewRequest("GET", webfingerURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating WebFinger request: %v", err)
	}

	// Set appropriate headers for WebFinger
	req.Header.Set("Accept", "application/jrd+json, application/json")
	req.Header.Set("User-Agent", UserAgent)

	// Fetch WebFinger data
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error fetching WebFinger data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("WebFinger request failed with status: %s", resp.Status)
	}

	// Read and parse the WebFinger response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading WebFinger response: %v", err)
	}

	fmt.Printf("WebFinger response content type: %s\n", resp.Header.Get("Content-Type"))
	fmt.Printf("WebFinger response body: %s\n", string(body))

	var webfinger WebFingerResponse
	if err := json.Unmarshal(body, &webfinger); err != nil {
		return "", fmt.Errorf("error decoding WebFinger response: %v", err)
	}

	// Find the ActivityPub actor URL
	var actorURL string

	// First try to find a link with rel="self" and type containing "activity+json"
	for _, link := range webfinger.Links {
		if link.Rel == "self" && strings.Contains(link.Type, "activity+json") {
			actorURL = link.Href
			fmt.Printf("Found ActivityPub actor URL with type %s: %s\n", link.Type, actorURL)
			break
		}
	}

	// If not found, try with rel="self" and any type
	if actorURL == "" {
		for _, link := range webfinger.Links {
			if link.Rel == "self" {
				actorURL = link.Href
				fmt.Printf("Found ActivityPub actor URL with rel=self: %s\n", actorURL)
				break
			}
		}
	}

	// If still not found, try with any link that might be useful
	if actorURL == "" {
		for _, link := range webfinger.Links {
			if link.Rel == "http://webfinger.net/rel/profile-page" {
				actorURL = link.Href
				fmt.Printf("Using profile page as fallback: %s\n", actorURL)
				break
			}
		}
	}

	if actorURL == "" {
		return "", fmt.Errorf("could not find any suitable URL in WebFinger response")
	}

	// Now fetch the actor data
	return r.fetchActivityPubObject(actorURL)
}

// resolveURL resolves a Fediverse URL to its ActivityPub representation
func (r *Resolver) resolveURL(inputURL string) (string, error) {
	// Parse the URL
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %v", err)
	}

	// For cross-instance URLs, we'll skip the redirect check
	// because some instances (like Mastodon) have complex redirect systems
	// that might not work reliably

	// Check if this is a cross-instance URL (e.g., https://mastodon.social/@user@another.instance/123)
	username := parsedURL.Path
	if len(username) > 0 && username[0] == '/' {
		username = username[1:]
	}

	// Check if the username contains an @ symbol (indicating a cross-instance URL)
	if strings.HasPrefix(username, "@") && strings.Contains(username[1:], "@") {
		// This is a cross-instance URL
		fmt.Println("Detected cross-instance URL. Original instance:", strings.Split(username[1:], "/")[0])

		// Extract the original instance, username, and post ID
		parts := strings.Split(username, "/")
		if len(parts) >= 2 {
			userParts := strings.Split(parts[0][1:], "@") // Remove the leading @ and split by @
			if len(userParts) == 2 {
				username := userParts[0]
				originalDomain := userParts[1]
				postID := parts[1]

				fmt.Printf("Detected cross-instance URL. Original instance: %s, username: %s, post ID: %s\n",
					originalDomain, username, postID)

				// Try different URL formats that are commonly used by different Fediverse platforms
				urlFormats := []string{
					// Mastodon format
					"https://%s/@%s/%s",
					"https://%s/users/%s/statuses/%s",
					// Pleroma format
					"https://%s/notice/%s",
					// Misskey format
					"https://%s/notes/%s",
					// Friendica format
					"https://%s/display/%s",
					// Hubzilla format
					"https://%s/item/%s",
				}

				// Try each URL format
				for _, format := range urlFormats {
					var targetURL string
					if strings.Count(format, "%s") == 3 {
						// Format with username
						targetURL = fmt.Sprintf(format, originalDomain, username, postID)
					} else {
						// Format without username (just domain and ID)
						targetURL = fmt.Sprintf(format, originalDomain, postID)
					}

					fmt.Printf("Trying URL format: %s\n", targetURL)

					// Try to fetch with our signature-first approach
					result, err := r.fetchActivityPubObject(targetURL)
					if err == nil {
						return result, nil
					}

					fmt.Printf("Failed with error: %v\n", err)

					// Add a delay between requests to avoid rate limiting
					fmt.Println("Waiting 2 seconds before trying next URL format...")
					time.Sleep(2 * time.Second)
				}

				// If all formats fail, return the last error
				return "", fmt.Errorf("failed to fetch content from original instance %s: all URL formats tried", originalDomain)
			}
		}
	}

	// If not a cross-instance URL, fetch the ActivityPub object directly
	return r.fetchActivityPubObject(inputURL)
}

// fetchActivityPubObject fetches an ActivityPub object from a URL
// This function now uses a signature-first approach by default
func (r *Resolver) fetchActivityPubObject(objectURL string) (string, error) {
	fmt.Printf("Fetching ActivityPub object from: %s\n", objectURL)

	// Make sure the URL is valid
	parsedURL, err := url.Parse(objectURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %v", err)
	}

	// Ensure the URL has a scheme
	if parsedURL.Scheme == "" {
		objectURL = "https://" + objectURL
	}

	// Use our signature-first approach by default
	return r.fetchActivityPubObjectWithSignature(objectURL)
}

// isBareDomain returns true if input is a domain or domain/ (no scheme, no @, no path beyond optional trailing slash, allows port)
var bareDomainRe = regexp.MustCompile(`^[a-zA-Z0-9.-]+(:[0-9]+)?/?$`)
func isBareDomain(input string) bool {
	if strings.Contains(input, "@") || strings.Contains(input, "://") {
		return false
	}
	return bareDomainRe.MatchString(input)
}
