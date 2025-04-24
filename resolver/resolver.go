package resolver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dennis/fediresolve/formatter"
	"github.com/tidwall/gjson"
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
		return "", fmt.Errorf("invalid URL: %v", err)
	}

	// Ensure the URL has a scheme
	if parsedURL.Scheme == "" {
		inputURL = "https://" + inputURL
		parsedURL, err = url.Parse(inputURL)
		if err != nil {
			return "", fmt.Errorf("invalid URL: %v", err)
		}
	}

	// Try to fetch the ActivityPub object directly
	return r.fetchActivityPubObject(inputURL)
}

// fetchActivityPubObject fetches an ActivityPub object from a URL
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
		parsedURL, err = url.Parse(objectURL)
		if err != nil {
			return "", fmt.Errorf("invalid URL: %v", err)
		}
	}
	
	// Create the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}

	// Set Accept headers to request ActivityPub data
	// Use multiple Accept headers to increase compatibility with different servers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", "FediResolve/1.0 (https://github.com/dennis/fediresolve)")

	// Perform the request
	fmt.Printf("Sending request with headers: %v\n", req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error fetching ActivityPub data: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("Received response with status: %s\n", resp.Status)
	if resp.StatusCode != http.StatusOK {
		// Try to read the error response body for debugging
		errorBody, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("ActivityPub request failed with status: %s\nResponse body: %s", 
			resp.Status, string(errorBody))
	}

	// Read and parse the ActivityPub response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading ActivityPub response: %v", err)
	}
	
	// Debug output
	fmt.Printf("ActivityPub response content type: %s\n", resp.Header.Get("Content-Type"))
	
	// Check if the response is empty
	if len(body) == 0 {
		return "", fmt.Errorf("received empty response body")
	}
	
	// Try to decode the JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// If we can't parse as JSON, return the raw response for debugging
		return "", fmt.Errorf("error decoding ActivityPub response: %v\nResponse body: %s", 
			err, string(body))
	}

	// Check if this is a shared/forwarded object and we need to fetch the original
	jsonData, _ := json.Marshal(data)
	jsonStr := string(jsonData)

	// Check for various ActivityPub types that might reference an original object
	if gjson.Get(jsonStr, "type").String() == "Announce" {
		// This is a boost/share, get the original object
		originalURL := gjson.Get(jsonStr, "object").String()
		if originalURL != "" && (strings.HasPrefix(originalURL, "http://") || strings.HasPrefix(originalURL, "https://")) {
			fmt.Printf("Found Announce, following original at: %s\n", originalURL)
			return r.fetchActivityPubObject(originalURL)
		}
	}

	// Format the result
	return formatter.Format(data)
}
