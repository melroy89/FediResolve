package resolver

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/dennis/fediresolve/formatter"
	"github.com/go-fed/httpsig"
	"github.com/tidwall/gjson"
)

// Define common constants
const (
	// UserAgent is the user agent string used for all HTTP requests
	UserAgent = "FediResolve/1.0 (https://melroy.org)"
)

// fetchActivityPubObjectWithSignature is a helper function that always signs HTTP requests
// This is the preferred way to fetch ActivityPub content as many instances require signatures
func (r *Resolver) fetchActivityPubObjectWithSignature(objectURL string) (string, error) {
	fmt.Printf("Fetching ActivityPub object with HTTP signatures from: %s\n", objectURL)
	
	// First, we need to extract the actor URL from the object URL
	actorURL, err := r.extractActorURLFromObjectURL(objectURL)
	if err != nil {
		// If we can't extract the actor URL, fall back to a direct request
		fmt.Printf("Could not extract actor URL: %v, falling back to direct request\n", err)
		return r.fetchActivityPubObjectDirect(objectURL)
	}
	
	// Then, we need to fetch the actor data to get the public key
	actorData, err := r.fetchActorData(actorURL)
	if err != nil {
		// If we can't fetch the actor data, fall back to a direct request
		fmt.Printf("Could not fetch actor data: %v, falling back to direct request\n", err)
		return r.fetchActivityPubObjectDirect(objectURL)
	}
	
	// Extract the public key ID 
	keyID, _, err := r.extractPublicKey(actorData)
	if err != nil {
		// If we can't extract the public key, fall back to a direct request
		fmt.Printf("Could not extract public key: %v, falling back to direct request\n", err)
		return r.fetchActivityPubObjectDirect(objectURL)
	}
	
	// Create a new private key for signing (in a real app, we would use a persistent key)
	privateKey, err := generateRSAKey()
	if err != nil {
		// If we can't generate a key, fall back to a direct request
		fmt.Printf("Could not generate RSA key: %v, falling back to direct request\n", err)
		return r.fetchActivityPubObjectDirect(objectURL)
	}
	
	// Now, sign and send the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating signed request: %v", err)
	}
	
	// Set headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	
	// Sign the request
	if err := signRequest(req, keyID, privateKey); err != nil {
		// If we can't sign the request, fall back to a direct request
		fmt.Printf("Could not sign request: %v, falling back to direct request\n", err)
		return r.fetchActivityPubObjectDirect(objectURL)
	}
	
	// Send the request
	fmt.Printf("Sending signed request with headers: %v\n", req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending signed request: %v", err)
	}
	defer resp.Body.Close()
	
	fmt.Printf("Received response with status: %s\n", resp.Status)
	if resp.StatusCode != http.StatusOK {
		// If the signed request fails, try a direct request as a fallback
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			fmt.Println("Signed request failed with auth error, trying direct request as fallback")
			return r.fetchActivityPubObjectDirect(objectURL)
		}
		
		// Read body for error info
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("signed request failed with status: %s, body: %s", resp.Status, string(body))
	}
	
	// Read and parse the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}
	
	// Debug output
	fmt.Printf("Response content type: %s\n", resp.Header.Get("Content-Type"))
	
	// Check if the response is empty
	if len(body) == 0 {
		return "", fmt.Errorf("received empty response body")
	}
	
	// Try to decode the JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("error decoding response: %v", err)
	}
	
	// Format the result
	return formatter.Format(data)
}

// fetchActivityPubObjectDirect is a helper function to fetch content without signatures
// This is used as a fallback when signing fails
func (r *Resolver) fetchActivityPubObjectDirect(objectURL string) (string, error) {
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
		return "", fmt.Errorf("error creating request: %v", err)
	}

	// Set Accept headers to request ActivityPub data
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)

	// Perform the request
	fmt.Printf("Sending direct request with headers: %v\n", req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error fetching content: %v", err)
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
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status: %s, body: %s", resp.Status, string(body))
	}

	// Read and parse the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}
	
	// Debug output
	fmt.Printf("Response content type: %s\n", resp.Header.Get("Content-Type"))
	
	// Check if the response is empty
	if len(body) == 0 {
		return "", fmt.Errorf("received empty response body")
	}
	
	// Try to decode the JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("error decoding response: %v", err)
	}

	// Format the result
	return formatter.Format(data)
}

// fetchWithSignature fetches ActivityPub content using HTTP Signatures
func (r *Resolver) fetchWithSignature(objectURL string) (string, error) {
	fmt.Printf("Fetching with HTTP signatures from: %s\n", objectURL)
	
	// First, we need to extract the actor URL from the object URL
	actorURL, err := r.extractActorURLFromObjectURL(objectURL)
	if err != nil {
		return "", fmt.Errorf("error extracting actor URL: %v", err)
	}
	
	// Then, we need to fetch the actor data to get the public key
	actorData, err := r.fetchActorData(actorURL)
	if err != nil {
		return "", fmt.Errorf("error fetching actor data: %v", err)
	}
	
	// Extract the public key ID 
	keyID, _, err := r.extractPublicKey(actorData)
	if err != nil {
		return "", fmt.Errorf("error extracting public key: %v", err)
	}
	
	// Create a new private key for signing (in a real app, we would use a persistent key)
	privateKey, err := generateRSAKey()
	if err != nil {
		return "", fmt.Errorf("error generating RSA key: %v", err)
	}
	
	// Now, sign and send the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return "", fmt.Errorf("error creating signed request: %v", err)
	}
	
	// Set headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	
	// Sign the request
	if err := signRequest(req, keyID, privateKey); err != nil {
		return "", fmt.Errorf("error signing request: %v", err)
	}
	
	// Send the request
	fmt.Printf("Sending signed request with headers: %v\n", req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending signed request: %v", err)
	}
	defer resp.Body.Close()
	
	fmt.Printf("Received response with status: %s\n", resp.Status)
	if resp.StatusCode != http.StatusOK {
		// Read body for error info
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("signed request failed with status: %s, body: %s", resp.Status, string(body))
	}
	
	// Read and parse the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}
	
	// Debug output
	fmt.Printf("Response content type: %s\n", resp.Header.Get("Content-Type"))
	
	// Check if the response is empty
	if len(body) == 0 {
		return "", fmt.Errorf("received empty response body")
	}
	
	// Try to decode the JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("error decoding response: %v", err)
	}
	
	// Format the result
	return formatter.Format(data)
}

// extractActorURLFromObjectURL extracts the actor URL from an object URL
func (r *Resolver) extractActorURLFromObjectURL(objectURL string) (string, error) {
	// This is a simplified approach - in a real app, we would parse the object URL properly
	// For now, we'll assume the actor URL is the base domain with the username
	
	// Basic URL pattern: https://domain.tld/@username/postid
	parts := strings.Split(objectURL, "/")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid object URL format: %s", objectURL)
	}
	
	// Extract domain and username
	domain := parts[2]
	username := parts[3]
	
	// Handle different URL formats
	if strings.HasPrefix(username, "@") {
		// Format: https://domain.tld/@username/postid
		username = strings.TrimPrefix(username, "@")
		
		// Check for cross-instance handles like @user@domain.tld
		if strings.Contains(username, "@") {
			userParts := strings.Split(username, "@")
			if len(userParts) == 2 {
				username = userParts[0]
				domain = userParts[1]
			}
		}
		
		// Try common URL patterns
		actorURLs := []string{
			fmt.Sprintf("https://%s/users/%s", domain, username),
			fmt.Sprintf("https://%s/@%s", domain, username),
			fmt.Sprintf("https://%s/user/%s", domain, username),
			fmt.Sprintf("https://%s/accounts/%s", domain, username),
			fmt.Sprintf("https://%s/profile/%s", domain, username),
		}
		
		// Try each URL pattern
		for _, actorURL := range actorURLs {
			fmt.Printf("Trying potential actor URL: %s\n", actorURL)
			// Check if this URL returns a valid actor
			actorData, err := r.fetchActorData(actorURL)
			if err == nil && actorData != nil {
				return actorURL, nil
			}
			
			// Add a small delay between requests to avoid rate limiting
			fmt.Println("Waiting 1 second before trying next actor URL...")
			time.Sleep(1 * time.Second)
		}
		
		// If we couldn't find a valid actor URL, try WebFinger
		fmt.Printf("Trying WebFinger resolution for: %s@%s\n", username, domain)
		return r.resolveActorViaWebFinger(username, domain)
	} else if username == "users" || username == "user" || username == "accounts" || username == "profile" {
		// Format: https://domain.tld/users/username/postid
		if len(parts) < 5 {
			return "", fmt.Errorf("invalid user URL format: %s", objectURL)
		}
		actorURL := fmt.Sprintf("https://%s/%s/%s", domain, username, parts[4])
		return actorURL, nil
	}
	
	// If we get here, we couldn't determine the actor URL
	return "", fmt.Errorf("could not determine actor URL from: %s", objectURL)
}

// fetchActorData fetches actor data from an actor URL
func (r *Resolver) fetchActorData(actorURL string) (map[string]interface{}, error) {
	fmt.Printf("Fetching actor data from: %s\n", actorURL)
	
	// Create the request
	req, err := http.NewRequest("GET", actorURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}
	
	// Set headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\", application/json")
	req.Header.Set("User-Agent", UserAgent)
	
	// Send the request
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching actor data: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("actor request failed with status: %s", resp.Status)
	}
	
	// Read and parse the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading actor response: %v", err)
	}
	
	// Parse JSON
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("error parsing actor data: %v", err)
	}
	
	return data, nil
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
	return rsa.GenerateKey(strings.NewReader("fediresolve-demo-random-source"), 2048)
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
	body, err := ioutil.ReadAll(resp.Body)
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
