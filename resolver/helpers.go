package resolver

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-fed/httpsig"
	"github.com/tidwall/gjson"
	"gitlab.melroy.org/melroy/fediresolve/formatter"
)

// Define common constants
const (
	// UserAgent is the user agent string used for all HTTP requests
	UserAgent    = "FediResolve/1.0 (https://github.com/melroy89/FediResolve)"
	AcceptHeader = "application/activity+json, application/ld+json"
)

// fetchActivityPubObjectWithSignature is a helper function that always signs HTTP requests
// This is the preferred way to fetch ActivityPub content as many instances require signatures
func (r *Resolver) fetchActivityPubObjectWithSignature(objectURL string) ([]byte, error) {
	fmt.Printf("Fetching ActivityPub object with HTTP signatures from: %s\n", objectURL)

	// Fetch the object itself
	data, err := r.fetchActivityPubObjectDirect(objectURL)
	if err != nil {
		return nil, err
	}

	// Extract the public key ID
	keyID, err := r.extractPublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("could not extract public key: %v", err)
	}

	// Create a new private key for signing (in a real app, we would use a persistent key)
	privateKey, err := generateRSAKey()
	if err != nil {
		return nil, fmt.Errorf("could not generate RSA key: %v", err)
	}

	// Now, sign and send the request
	req, err := http.NewRequest("GET", objectURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating signed request: %v", err)
	}

	// Set headers
	req.Header.Set("Accept", AcceptHeader)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	// Sign the request
	if err := signRequest(req, keyID, privateKey); err != nil {
		return nil, fmt.Errorf("could not sign request: %v", err)
	}

	// Send the request
	fmt.Printf("Sending signed request with headers: %v\n", req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending signed request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("signed request failed with status: %s, body: %s", resp.Status, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}
	if len(bodyBytes) == 0 {
		return nil, fmt.Errorf("received empty response body")
	}

	return bodyBytes, nil
}

// fetchActivityPubObjectDirect is a helper function to fetch content without signatures
// This is used as a fallback when signing fails
func (r *Resolver) fetchActivityPubObjectDirect(objectURL string) ([]byte, error) {
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
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	// Set Accept headers to request ActivityPub data
	req.Header.Set("Accept", AcceptHeader)
	req.Header.Set("User-Agent", UserAgent)

	// Perform the request
	fmt.Printf("Sending direct request with headers: %v\n", req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching content: %v", err)
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
		return nil, fmt.Errorf("request failed with status: %s, body: %s", resp.Status, string(body))
	}

	// Read and parse the response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	// Debug output
	fmt.Printf("Response content type: %s\n", resp.Header.Get("Content-Type"))

	// Check if the response is empty
	if len(bodyBytes) == 0 {
		return nil, fmt.Errorf("received empty response body")
	}

	return bodyBytes, nil
}

// fetchActorData fetches actor data from an actor URL
func (r *Resolver) fetchActorData(actorURL string) ([]byte, error) {
	fmt.Printf("Fetching actor data from: %s\n", actorURL)

	// Create the request
	req, err := http.NewRequest("GET", actorURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	// Set headers
	req.Header.Set("Accept", AcceptHeader)
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
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading actor response: %v", err)
	}

	return bodyBytes, nil
}

// extractPublicKey extracts the public key ID from actor data
func (r *Resolver) extractPublicKey(data []byte) (string, error) {
	// Try to find the attributedTo URL
	actorURL := gjson.GetBytes(data, "attributedTo").String()

	if actorURL == "" {
		fmt.Printf("Could not find attributedTo in object\n")
		// Try to find key in the object itself
		keyID := gjson.GetBytes(data, "publicKey.id").String()

		if keyID == "" {
			return "", fmt.Errorf("could not find public key ID in object")
		}
		return keyID, nil
	} else {
		actorData, err := r.fetchActorData(actorURL)
		if err != nil {
			return "", fmt.Errorf("error fetching actor data: %v", err)
		}

		// Extract key ID
		keyID := gjson.GetBytes(actorData, "publicKey.id").String()
		if keyID == "" {
			// Try alternate formats
			keyID = gjson.GetBytes(actorData, "publicKey.0.id").String()
		}
		if keyID == "" {
			fmt.Printf("could not find public key ID in actor data")
			return "dummy", nil
		}
		return keyID, nil
	}
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

// fetchNodeInfo fetches nodeinfo from the given domain, returning the raw JSON
func (r *Resolver) fetchNodeInfo(domain string) ([]byte, error) {
	nodeinfoURL := "https://" + domain + "/.well-known/nodeinfo"
	fmt.Printf("Fetching nodeinfo discovery from: %s\n", nodeinfoURL)

	resp, err := r.client.Get(nodeinfoURL)
	if err != nil {
		return nil, fmt.Errorf("error fetching nodeinfo discovery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodeinfo discovery failed with status: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading nodeinfo discovery: %v", err)
	}

	var nodeinfoHref string
	result := gjson.GetBytes(body, `links.#(rel%"*/schema/2.1").href`)
	if !result.Exists() {
		result = gjson.GetBytes(body, `links.#(rel%"*/schema/2.0").href`)
	}
	if result.Exists() {
		nodeinfoHref = result.String()
	}
	if nodeinfoHref == "" {
		return nil, fmt.Errorf("no nodeinfo schema 2.1 or 2.0 found")
	}

	fmt.Printf("Fetching nodeinfo from: %s\n", nodeinfoHref)
	resp2, err := r.client.Get(nodeinfoHref)
	if err != nil {
		return nil, fmt.Errorf("error fetching nodeinfo: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodeinfo fetch failed with status: %s", resp2.Status)
	}
	raw, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading nodeinfo: %v", err)
	}
	return raw, nil
}

// Try to extract actor, else try nodeinfo fallback for top-level domains
func (r *Resolver) ResolveObjectOrNodeInfo(objectURL string) ([]byte, error) {
	// If actor resolution fails, try nodeinfo
	parts := strings.Split(objectURL, "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid object URL: %s", objectURL)
	}
	domain := parts[2]
	body, err := r.fetchNodeInfo(domain)
	if err != nil {
		return nil, fmt.Errorf("could not fetch nodeinfo: %v", err)
	}
	return body, nil
}

// Format result
func formatResult(raw []byte) (string, error) {
	return formatter.Format(raw)
}
