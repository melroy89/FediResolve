package formatter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	h2m "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/fatih/color"
	"github.com/tidwall/gjson"
	markdown "github.com/vlanse/go-term-markdown"
)

// Format takes ActivityPub data and returns a formatted string representation
func Format(data map[string]interface{}) (string, error) {
	// First, get the beautified JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("error formatting JSON: %v", err)
	}

	// Convert to string for gjson parsing
	jsonStr := string(jsonData)

	// Create a summary based on the object type
	summary := createSummary(jsonStr)

	// Combine the full JSON first, followed by the summary at the bottom
	result := fmt.Sprintf("%s\n\n%s", string(jsonData), summary)
	return result, nil
}

// createSummary generates a human-readable summary of the ActivityPub object
func createSummary(jsonStr string) string {
	objectType := gjson.Get(jsonStr, "type").String()

	// Build a header with the object type
	bold := color.New(color.Bold).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	header := fmt.Sprintf("%s: %s\n", bold("Type"), cyan(objectType))

	// Add common fields
	var summaryParts []string
	summaryParts = append(summaryParts, header)

	// Add ID if available
	if id := gjson.Get(jsonStr, "id").String(); id != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("%s: %s", bold("ID"), green(id)))
	}

	// Process based on type
	switch objectType {
	case "Person", "Application", "Group", "Organization", "Service":
		summaryParts = formatActor(jsonStr, summaryParts, bold, cyan, green, red, yellow)
	case "Note", "Article", "Page", "Question":
		summaryParts = formatContent(jsonStr, summaryParts, bold, green, yellow)
	case "Create", "Update", "Delete", "Follow", "Add", "Remove", "Like", "Block", "Announce":
		summaryParts = formatActivity(jsonStr, summaryParts, bold, green, yellow)
	case "Collection", "OrderedCollection", "CollectionPage", "OrderedCollectionPage":
		summaryParts = formatCollection(jsonStr, summaryParts, bold, green, yellow)
	case "Image", "Audio", "Video", "Document":
		summaryParts = formatMedia(jsonStr, summaryParts, bold, green, yellow)
	case "Event":
		summaryParts = formatEvent(jsonStr, summaryParts, bold, yellow)
	case "Tombstone":
		summaryParts = formatTombstone(jsonStr, summaryParts, bold, green, yellow)
	}

	return strings.Join(summaryParts, "\n")
}

// formatActor formats actor-type objects (Person, Service, etc.)
func formatActor(jsonStr string, parts []string, bold, cyan, green, red, yellow func(a ...interface{}) string) []string {
	if name := gjson.Get(jsonStr, "name").String(); name != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Name"), cyan(name)))
	}

	if preferredUsername := gjson.Get(jsonStr, "preferredUsername").String(); preferredUsername != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Username"), red(preferredUsername)))
	}

	if url := gjson.Get(jsonStr, "url").String(); url != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("URL"), green(url)))
	}

	iconUrl := gjson.Get(jsonStr, "icon.url").String()
	if iconUrl != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Avatar"), green(iconUrl)))
	}

	if summary := gjson.Get(jsonStr, "summary").String(); summary != "" {
		md := htmlToMarkdown(summary)
		parts = append(parts, fmt.Sprintf("%s:\n%s", bold("Summary"), renderMarkdown(md)))
	}

	if published := gjson.Get(jsonStr, "published").String(); published != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Published"), yellow(formatDate(published))))
	}

	if followers := gjson.Get(jsonStr, "followers").String(); followers != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Followers"), green(followers)))
	}

	if following := gjson.Get(jsonStr, "following").String(); following != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Following"), green(following)))
	}

	return parts
}

// formatContent formats content-type objects (Note, Article, Page, etc.)
func formatContent(jsonStr string, parts []string, bold, green, yellow func(a ...interface{}) string) []string {
	// Show the name/title if present (especially for Page/thread)
	if name := gjson.Get(jsonStr, "name").String(); name != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Title"), name))
	}

	if content := gjson.Get(jsonStr, "content").String(); content != "" {
		md := htmlToMarkdown(content)
		// Truncate the content if its too big.
		if len(md) > 1200 {
			md = md[:1197] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s:\n%s", bold("Content"), renderMarkdown(md)))
	}

	// Check for attachments (images, videos, etc.)
	attachments := gjson.Get(jsonStr, "attachment").Array()
	if len(attachments) > 0 {
		parts = append(parts, fmt.Sprintf("%s:", bold("Attachments")))
		for i, attachment := range attachments {
			attachmentType := attachment.Get("type").String()
			mediaType := attachment.Get("mediaType").String()
			url := attachment.Get("url").String()
			href := attachment.Get("href").String()
			name := attachment.Get("name").String()

			// Truncate long names
			if len(name) > 100 {
				name = name[:97] + "..."
			}

			attachmentInfo := fmt.Sprintf("  %d. %s", i+1, green(attachmentType))
			if mediaType != "" {
				attachmentInfo += fmt.Sprintf(" (%s)", mediaType)
			}
			if name != "" {
				attachmentInfo += fmt.Sprintf(": %s", name)
			}
			parts = append(parts, attachmentInfo)

			// For type Page and attachment type Link, show href if present
			objectType := gjson.Get(jsonStr, "type").String()
			if objectType == "Page" && attachmentType == "Link" && href != "" {
				parts = append(parts, fmt.Sprintf("     URL: %s", green(href)))
			} else if url != "" {
				parts = append(parts, fmt.Sprintf("     URL: %s", green(url)))
			}
		}
	}

	if published := gjson.Get(jsonStr, "published").String(); published != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Published"), yellow(formatDate(published))))
	}

	if updated := gjson.Get(jsonStr, "updated").String(); updated != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Updated"), yellow(formatDate(updated))))
	}

	if attributedTo := gjson.Get(jsonStr, "attributedTo").String(); attributedTo != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Author"), green(attributedTo)))
	}

	if to := gjson.Get(jsonStr, "to").Array(); len(to) > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("To"), green(formatArray(to))))
	}

	if cc := gjson.Get(jsonStr, "cc").Array(); len(cc) > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("CC"), green(formatArray(cc))))
	}

	if inReplyTo := gjson.Get(jsonStr, "inReplyTo").String(); inReplyTo != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("In Reply To"), green(inReplyTo)))
	}

	// Include endTime for Question type
	if endTime := gjson.Get(jsonStr, "endTime").String(); endTime != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("End Time"), yellow(formatDate(endTime))))
	}

	// Include options (oneOf/anyOf) for Question type
	options := gjson.Get(jsonStr, "oneOf").Array()
	if len(options) == 0 {
		options = gjson.Get(jsonStr, "anyOf").Array()
	}
	if len(options) > 0 {
		parts = append(parts, fmt.Sprintf("%s:", bold("Poll Options")))
		for i, opt := range options {
			name := opt.Get("name").String()
			parts = append(parts, fmt.Sprintf("  %d. %s", i+1, name))
		}
	}

	return parts
}

// formatActivity formats activity-type objects (Create, Like, etc.)
func formatActivity(jsonStr string, parts []string, bold, green, yellow func(a ...interface{}) string) []string {
	if actor := gjson.Get(jsonStr, "actor").String(); actor != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Actor"), actor))
	}

	if object := gjson.Get(jsonStr, "object").String(); object != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Object"), object))
	} else if gjson.Get(jsonStr, "object").IsObject() {
		objectType := gjson.Get(jsonStr, "object.type").String()
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Object Type"), yellow(objectType)))

		if content := gjson.Get(jsonStr, "object.content").String(); content != "" {
			md := htmlToMarkdown(content)
			parts = append(parts, fmt.Sprintf("%s:\n%s", bold("Content"), renderMarkdown(md)))
		}

		// Check for attachments in the object
		attachments := gjson.Get(jsonStr, "object.attachment").Array()
		if len(attachments) > 0 {
			parts = append(parts, fmt.Sprintf("%s:", bold("Attachments")))
			for i, attachment := range attachments {
				attachmentType := attachment.Get("type").String()
				mediaType := attachment.Get("mediaType").String()
				url := attachment.Get("url").String()
				name := attachment.Get("name").String()

				// Truncate long names
				if len(name) > 100 {
					name = name[:97] + "..."
				}

				attachmentInfo := fmt.Sprintf("  %d. %s", i+1, green(attachmentType))
				if mediaType != "" {
					attachmentInfo += fmt.Sprintf(" (%s)", mediaType)
				}
				if name != "" {
					attachmentInfo += fmt.Sprintf(": %s", name)
				}
				parts = append(parts, attachmentInfo)

				if url != "" {
					parts = append(parts, fmt.Sprintf("     URL: %s", url))
				}
			}
		}
	}

	if published := gjson.Get(jsonStr, "published").String(); published != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Published"), yellow(formatDate(published))))
	}

	if target := gjson.Get(jsonStr, "target").String(); target != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Target"), target))
	}

	return parts
}

// formatCollection formats collection-type objects
func formatCollection(jsonStr string, parts []string, bold, green, yellow func(a ...interface{}) string) []string {
	if totalItems := gjson.Get(jsonStr, "totalItems").Int(); totalItems > 0 {
		parts = append(parts, fmt.Sprintf("%s: %d", bold("Total Items"), totalItems))
	}

	// Show first few items if available
	items := gjson.Get(jsonStr, "items").Array()
	if len(items) == 0 {
		items = gjson.Get(jsonStr, "orderedItems").Array()
	}

	if len(items) > 0 {
		itemCount := len(items)
		if itemCount > 3 {
			itemCount = 3
		}

		parts = append(parts, fmt.Sprintf("%s:", bold("First Items")))
		for i := 0; i < itemCount; i++ {
			item := items[i].String()
			if len(item) > 100 {
				item = item[:97] + "..."
			}
			parts = append(parts, fmt.Sprintf("  - %s", item))
		}

		if len(items) > 3 {
			parts = append(parts, fmt.Sprintf("  ... and %d more items", len(items)-3))
		}
	}

	if published := gjson.Get(jsonStr, "published").String(); published != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Published"), yellow(formatDate(published))))
	}

	return parts
}

// formatMedia formats media-type objects (Image, Video, etc.)
func formatMedia(jsonStr string, parts []string, bold, green, yellow func(a ...interface{}) string) []string {
	if name := gjson.Get(jsonStr, "name").String(); name != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Title"), name))
	}

	if url := gjson.Get(jsonStr, "url").String(); url != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("URL"), green(url)))
	}

	if duration := gjson.Get(jsonStr, "duration").String(); duration != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Duration"), duration))
	}

	if published := gjson.Get(jsonStr, "published").String(); published != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Published"), yellow(formatDate(published))))
	}

	if attributedTo := gjson.Get(jsonStr, "attributedTo").String(); attributedTo != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Author"), attributedTo))
	}

	return parts
}

// formatEvent formats event-type objects
func formatEvent(jsonStr string, parts []string, bold, yellow func(a ...interface{}) string) []string {
	if name := gjson.Get(jsonStr, "name").String(); name != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Title"), name))
	}

	if content := gjson.Get(jsonStr, "content").String(); content != "" {
		md := htmlToMarkdown(content)
		parts = append(parts, fmt.Sprintf("%s:\n%s", bold("Description"), renderMarkdown(md)))
	}

	if startTime := gjson.Get(jsonStr, "startTime").String(); startTime != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Start Time"), yellow(formatDate(startTime))))
	}

	if endTime := gjson.Get(jsonStr, "endTime").String(); endTime != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("End Time"), yellow(formatDate(endTime))))
	}

	if location := gjson.Get(jsonStr, "location").String(); location != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Location"), location))
	}

	return parts
}

// formatTombstone formats tombstone-type objects
func formatTombstone(jsonStr string, parts []string, bold, green, yellow func(a ...interface{}) string) []string {
	if formerType := gjson.Get(jsonStr, "formerType").String(); formerType != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Former Type"), formerType))
	}

	if deleted := gjson.Get(jsonStr, "deleted").String(); deleted != "" {
		parts = append(parts, fmt.Sprintf("%s: %s", bold("Deleted"), yellow(formatDate(deleted))))
	}

	return parts
}

// Helper to convert HTML to Markdown and render to terminal
func renderMarkdown(md string) string {
	// width=80, no color override, no emoji, no images
	return string(markdown.Render(md, 78, 2))
}

// Replace stripHTML with htmlToMarkdown
func htmlToMarkdown(html string) string {
	converter := h2m.NewConverter("", true, nil)
	md, err := converter.ConvertString(html)
	if err != nil {
		return html // fallback to original HTML if conversion fails
	}
	return md
}

// formatDate formats an ISO 8601 date string to a more readable format
func formatDate(isoDate string) string {
	t, err := time.Parse(time.RFC3339, isoDate)
	if err != nil {
		return isoDate
	}
	return t.Format("Jan 02, 2006 15:04:05")
}

// formatArray formats an array of values into a readable string
func formatArray(values []gjson.Result) string {
	if len(values) == 0 {
		return ""
	}

	var items []string
	for _, v := range values {
		items = append(items, v.String())
	}

	if len(items) == 1 {
		return items[0]
	}

	return fmt.Sprintf("[\n    %s\n  ]", strings.Join(items, ",\n    "))
}
