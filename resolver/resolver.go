package resolver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	// Check if input looks like a URL
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		fmt.Println("Detected URL, attempting direct resolution")
		return r.resolveURL(input)
	}
	
	// Check if input looks like a Fediverse handle (@username@domain.tld)
	if strings.Contains(input, "@") {
		// Handle format should be either @username@domain.tld or username@domain.tld
		// and should not contain any slashes or other URL-like characters
		if !strings.Contains(input, "/") && !strings.Contains(input, ":") {
			if strings.HasPrefix(input, "@") {
				// Format: @username@domain.tld
				if strings.Count(input, "@") == 2 {
					fmt.Println("Detected Fediverse handle, using WebFinger resolution")
					return r.resolveHandle(input)
				}
			} else {
				// Format: username@domain.tld
				if strings.Count(input, "@") == 1 {
					fmt.Println("Detected Fediverse handle, using WebFinger resolution")
					return r.resolveHandle(input)
				}
			}
		}
	}

	// If we're not sure, try to treat it as a URL
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
	req.Header.Set("User-Agent", "FediResolve/1.0 (https://github.com/dennis/fediresolve)")
	
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
	body, err := ioutil.ReadAll(resp.Body)
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
				
				// Try different URL formats that might work for the original instance
				formats := []string{
					"https://%s/@%s/%s",
					"https://%s/users/%s/statuses/%s",
					"https://%s/notes/%s",
					"https://%s/notice/%s",
				}
				
				for _, format := range formats {
					originalURL := fmt.Sprintf(format, originalDomain, username, postID)
					fmt.Printf("Attempting to fetch from original instance: %s\n", originalURL)
					
					// Try to fetch directly first
					fmt.Printf("Trying with ActivityPub direct fetch: %s\n", originalURL)
					result, err := r.fetchActivityPubObject(originalURL)
					if err == nil {
						return result, nil
					}
					
					// If direct fetch fails and it's an auth error, try with HTTP signatures
					if strings.Contains(err.Error(), "401 Unauthorized") || strings.Contains(err.Error(), "403 Forbidden") {
						fmt.Printf("Direct fetch failed with auth error, trying with HTTP signatures: %s\n", originalURL)
						result, sigErr := r.fetchWithSignature(originalURL)
						if sigErr == nil {
							return result, nil
						}
						fmt.Printf("HTTP signatures fetch also failed: %v\n", sigErr)
					}
					// If this fails, continue trying other formats
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
