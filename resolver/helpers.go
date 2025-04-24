package resolver

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-fed/httpsig"
	"github.com/tidwall/gjson"
	"gitlab.melroy.org/melroy/fediresolve/formatter"
)

// Define common constants
const (
	// UserAgent is the user agent string used for all HTTP requests
	UserAgent = "FediResolve/1.0 (https://melroy.org)"
)

// fetchActivityPubObjectWithSignature is a helper function that always signs HTTP requests
// This is the preferred way to fetch ActivityPub content as many instances require signatures
func (r *Resolver) fetchActivityPubObjectWithSignature(objectURL string) ([]byte, map[string]interface{}, error) {
	fmt.Printf("Fetching ActivityPub object with HTTP signatures from: %s\n", objectURL)

	// Fetch the object itself
	_, data, err := r.fetchActivityPubObjectDirect(objectURL)
	if err != nil {
		return nil, nil, err
	}

	var keyID string
	actorURL, ok := data["attributedTo"].(string)
	if !ok || actorURL == "" {
		fmt.Printf("Could not find attributedTo in object\n")
		// Try to find key in the object itself
		// Try to catch an error
		if _, ok := data["publicKey"].(map[string]interface{}); !ok {
			return nil, nil, fmt.Errorf("could not find public key in object")
		}
		if _, ok := data["publicKey"].(map[string]interface{})["id"]; !ok {
			return nil, nil, fmt.Errorf("could not find public key ID in object")
		}
		keyID = data["publicKey"].(map[string]interface{})["id"].(string)
	} else {
		// Fetch actor data
		_, actorData, err := r.fetchActorData(actorURL)
		if err != nil {
			return nil, nil, fmt.Errorf("could not fetch actor data: %v", err)
		}
		// Extract the public key ID
		key, _, err := r.extractPublicKey(actorData)
		if err != nil {
			return nil, nil, fmt.Errorf("could not extract public key: %v", err)
		}
		keyID = key
	}

	// Create a new private key for signing (in a real app, we would use a persistent key)
	privateKey, err := generateRSAKey()
	if err != nil {
		return nil, nil, fmt.Errorf("could not generate RSA key: %v", err)
	}

	// Now, sign and send the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating signed request: %v", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	// Sign the request
	if err := signRequest(req, keyID, privateKey); err != nil {
		return nil, nil, fmt.Errorf("could not sign request: %v", err)
	}

	// Send the request
	fmt.Printf("Sending signed request with headers: %v\n", req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("error sending signed request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("signed request failed with status: %s, body: %s", resp.Status, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading response: %v", err)
	}
	if len(bodyBytes) == 0 {
		return nil, nil, fmt.Errorf("received empty response body")
	}

	// Remove ANSI escape codes if present (some servers return colored output)
	cleanBody := removeANSIEscapeCodes(bodyBytes)

	// Try to decode the JSON response
	var body map[string]interface{}
	if err := json.Unmarshal(cleanBody, &body); err != nil {
		return nil, nil, fmt.Errorf("error decoding signed response: %v", err)
	}

	return bodyBytes, body, nil
}

// fetchActivityPubObjectDirect is a helper function to fetch content without signatures
// This is used as a fallback when signing fails
func (r *Resolver) fetchActivityPubObjectDirect(objectURL string) ([]byte, map[string]interface{}, error) {
	fmt.Printf("Fetching ActivityPub object directly from: %s\n", objectURL)

	// Create a custom client that doesn't follow redirects automatically
	// so we can capture the redirect URL
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Create the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating request: %v", err)
	}

	// Set Accept headers to request ActivityPub data
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"")
	req.Header.Set("User-Agent", UserAgent)

	// Perform the request
	fmt.Printf("Sending direct request with headers: %v\n", req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching content: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("Received response with status: %s\n", resp.Status)

	// Check if we got a redirect (302, 301, etc.)
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
		// Get the redirect URL from the Location header
		redirectURL := resp.Header.Get("Location")
		if redirectURL != "" {
			fmt.Printf("Found redirect to: %s\n", redirectURL)
			// Try to fetch the content from the redirect URL with HTTP signatures
			return r.fetchActivityPubObjectWithSignature(redirectURL)
		}
	}

	if resp.StatusCode != http.StatusOK {
		// Read body for error info
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("request failed with status: %s, body: %s", resp.Status, string(body))
	}

	// Read and parse the response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading response: %v", err)
	}

	// Debug output
	fmt.Printf("Response content type: %s\n", resp.Header.Get("Content-Type"))

	// Check if the response is empty
	if len(bodyBytes) == 0 {
		return nil, nil, fmt.Errorf("received empty response body")
	}

	// Remove ANSI escape codes if present (some servers return colored output)
	cleanBody := removeANSIEscapeCodes(bodyBytes)

	// Try to decode the JSON response
	var body map[string]interface{}
	if err := json.Unmarshal(cleanBody, &body); err != nil {
		return nil, nil, fmt.Errorf("error decoding response: %v", err)
	}

	return bodyBytes, body, nil
}

// fetchActorData fetches actor data from an actor URL
func (r *Resolver) fetchActorData(actorURL string) ([]byte, map[string]interface{}, error) {
	fmt.Printf("Fetching actor data from: %s\n", actorURL)

	// Create the request
	req, err := http.NewRequest("GET", actorURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating request: %v", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)

	// Send the request
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching actor data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("actor request failed with status: %s", resp.Status)
	}

	// Read and parse the response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading actor response: %v", err)
	}

	// Parse JSON
	var data map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return nil, nil, fmt.Errorf("error parsing actor data: %v", err)
	}

	return bodyBytes, data, nil
}

// extractPublicKey extracts the public key ID from actor data
func (r *Resolver) extractPublicKey(actorData map[string]interface{}) (string, string, error) {
	// Convert to JSON string for easier parsing with gjson
	actorJSON, err := json.Marshal(actorData)
	if err != nil {
		return "", "", fmt.Errorf("error marshaling actor data: %v", err)
	}

	// Extract key ID
	keyID := gjson.GetBytes(actorJSON, "publicKey.id").String()
	if keyID == "" {
		// Try alternate formats
		keyID = gjson.GetBytes(actorJSON, "publicKey.0.id").String()
	}
	if keyID == "" {
		return "", "", fmt.Errorf("could not find public key ID in actor data")
	}

	// For future implementation, we might need to parse and use the public key
	// But for now, we just return a dummy value since we're focused on signing
	dummyPEM := "dummy-key"

	return keyID, dummyPEM, nil
}

// generateRSAKey generates a new RSA key pair for signing requests
func generateRSAKey() (*rsa.PrivateKey, error) {
	// In a real app, we would use a persistent key, but for this demo, we'll generate a new one
	// For server-to-server communication, this is not ideal but works for demonstration purposes
	return rsa.GenerateKey(rand.Reader, 2048)
}

// signRequest signs an HTTP request using HTTP Signatures
func signRequest(req *http.Request, keyID string, privateKey *rsa.PrivateKey) error {
	// Make sure we have all required headers
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	// For GET requests with no body, we need to handle the digest differently
	if req.Body == nil {
		// Create an empty digest
		req.Header.Set("Digest", "SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
	}

	// Create a new signer with required headers for ActivityPub
	signer, _, err := httpsig.NewSigner(
		[]httpsig.Algorithm{httpsig.RSA_SHA256},
		httpsig.DigestSha256,
		[]string{"(request-target)", "host", "date", "digest"},
		httpsig.Signature,
		300, // 5 minute expiration
	)
	if err != nil {
		return fmt.Errorf("error creating signer: %v", err)
	}

	// Sign the request
	return signer.SignRequest(privateKey, keyID, req, nil)
}

// resolveActorViaWebFinger resolves an actor URL via WebFinger protocol
func (r *Resolver) resolveActorViaWebFinger(username, domain string) (string, error) {
	// WebFinger URL format: https://domain.tld/.well-known/webfinger?resource=acct:username@domain.tld
	webfingerURL := fmt.Sprintf("https://%s/.well-known/webfinger?resource=acct:%s@%s",
		domain, username, domain)

	fmt.Printf("Fetching WebFinger data from: %s\n", webfingerURL)

	// Create the request
	req, err := http.NewRequest("GET", webfingerURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating WebFinger request: %v", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/jrd+json, application/json")
	req.Header.Set("User-Agent", UserAgent)

	// Send the request
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error fetching WebFinger data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("WebFinger request failed with status: %s", resp.Status)
	}

	// Read and parse the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading WebFinger response: %v", err)
	}

	// Find the ActivityPub actor URL in the WebFinger response
	actorURL := ""
	webfingerData := gjson.ParseBytes(body)
	links := webfingerData.Get("links").Array()
	for _, link := range links {
		rel := link.Get("rel").String()
		typ := link.Get("type").String()
		href := link.Get("href").String()

		if rel == "self" && (typ == "application/activity+json" ||
			typ == "application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"" ||
			strings.Contains(typ, "activity+json")) {
			actorURL = href
			break
		}
	}

	if actorURL == "" {
		return "", fmt.Errorf("could not find ActivityPub actor URL in WebFinger response")
	}

	return actorURL, nil
}

// fetchNodeInfo fetches nodeinfo from the given domain, returning the raw JSON and parsed data
func (r *Resolver) fetchNodeInfo(domain string) ([]byte, map[string]interface{}, error) {
	nodeinfoURL := "https://" + domain + "/.well-known/nodeinfo"
	fmt.Printf("Fetching nodeinfo discovery from: %s\n", nodeinfoURL)

	resp, err := r.client.Get(nodeinfoURL)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching nodeinfo discovery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("nodeinfo discovery failed with status: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading nodeinfo discovery: %v", err)
	}
	var discovery struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, nil, fmt.Errorf("error parsing nodeinfo discovery: %v", err)
	}
	var nodeinfoHref string
	for _, link := range discovery.Links {
		if strings.HasSuffix(link.Rel, "/schema/2.1") {
			nodeinfoHref = link.Href
			break
		}
	}
	if nodeinfoHref == "" {
		for _, link := range discovery.Links {
			if strings.HasSuffix(link.Rel, "/schema/2.0") {
				nodeinfoHref = link.Href
				break
			}
		}
	}
	if nodeinfoHref == "" {
		return nil, nil, fmt.Errorf("no nodeinfo schema 2.1 or 2.0 found")
	}
	fmt.Printf("Fetching nodeinfo from: %s\n", nodeinfoHref)
	resp2, err := r.client.Get(nodeinfoHref)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching nodeinfo: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("nodeinfo fetch failed with status: %s", resp2.Status)
	}
	raw, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading nodeinfo: %v", err)
	}
	var nodeinfo map[string]interface{}
	if err := json.Unmarshal(raw, &nodeinfo); err != nil {
		return nil, nil, fmt.Errorf("error parsing nodeinfo: %v", err)
	}
	return raw, nodeinfo, nil
}

// Try to extract actor, else try nodeinfo fallback for top-level domains
func (r *Resolver) ResolveObjectOrNodeInfo(objectURL string) ([]byte, map[string]interface{}, string, error) {
	// If actor resolution fails, try nodeinfo
	parts := strings.Split(objectURL, "/")
	if len(parts) < 3 {
		return nil, nil, "", fmt.Errorf("invalid object URL: %s", objectURL)
	}
	domain := parts[2]
	raw, nodeinfo, err := r.fetchNodeInfo(domain)
	if err != nil {
		return nil, nil, "", fmt.Errorf("could not fetch nodeinfo: %v", err)
	}
	return raw, nodeinfo, "nodeinfo", nil
}

// FormatHelperResult wraps formatter.Format for use by resolver.go, keeping formatter import out of resolver.go
func FormatHelperResult(raw []byte, nodeinfo map[string]interface{}) (string, error) {
	return formatter.Format(nodeinfo)
}

// Try to always format (ideally the body data, or in the worse case the raw data to string)
func formatResult(raw []byte, data map[string]interface{}) string {
	formatted, err := formatter.Format(data)
	if err != nil {
		return string(raw)
	}
	return formatted
}

// removeANSIEscapeCodes strips ANSI escape codes from a byte slice
func removeANSIEscapeCodes(input []byte) []byte {
	ansiEscape := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiEscape.ReplaceAll(input, []byte{})
}
