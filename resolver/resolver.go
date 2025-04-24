package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		raw, err := r.ResolveObjectOrNodeInfo(parsedURL.String())
		if err != nil {
			return "", fmt.Errorf("error fetching nodeinfo: %v", err)
		}
		formatted, err := formatResult(raw)
		if err != nil {
			return "", fmt.Errorf("error formatting nodeinfo: %v", err)
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
	// Always fetch the provided URL as-is, using ActivityPub Accept header and HTTP signatures
	// Then, if the response contains an `id` field that differs from the requested URL, fetch that recursively
	return r.resolveCanonicalActivityPub(inputURL, 0)
}

// resolveCanonicalActivityPub fetches the ActivityPub object at the given URL, and if the response contains an `id` field
// that differs from the requested URL, recursively fetches that canonical URL. Max depth is used to prevent infinite loops.
func (r *Resolver) resolveCanonicalActivityPub(objectURL string, depth int) (string, error) {
	if depth > 3 {
		return "", fmt.Errorf("too many canonical redirects (possible loop)")
	}
	fmt.Printf("Fetching ActivityPub object for canonical resolution: %s\n", objectURL)
	jsonData, err := r.fetchActivityPubObjectRaw(objectURL)
	if err != nil {
		return "", err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return "", fmt.Errorf("error parsing ActivityPub JSON: %v", err)
	}
	idVal, ok := data["id"].(string)
	if ok && idVal != "" && idVal != objectURL {
		fmt.Printf("Found canonical id: %s (different from requested URL), following...\n", idVal)
		return r.resolveCanonicalActivityPub(idVal, depth+1)
	}
	// If no id or already canonical, format and return using helpers.go
	formatted, err := formatResult(jsonData)
	if err != nil {
		return "", fmt.Errorf("error formatting ActivityPub object: %v", err)
	}
	return formatted, nil
}

// fetchActivityPubObjectRaw fetches an ActivityPub object and returns the raw JSON []byte (not formatted)
func (r *Resolver) fetchActivityPubObjectRaw(objectURL string) ([]byte, error) {
	fmt.Printf("Fetching ActivityPub object with HTTP from: %s\n", objectURL)

	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating get request: %v", err)
	}
	req.Header.Set("Accept", "application/ld+json, application/activity+json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending get request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get request failed with status: %s, body: %s", resp.Status, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("received empty response body")
	}
	return body, nil
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
	raw, err := r.fetchActivityPubObjectWithSignature(objectURL)
	if err != nil {
		return "", fmt.Errorf("error fetching ActivityPub object: %v", err)
	}
	formatted, err := formatResult(raw)
	if err != nil {
		return "", fmt.Errorf("error formatting ActivityPub object: %v", err)
	}
	return formatted, nil
}
