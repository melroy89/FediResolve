# FediResolve

FediResolve is a command-line tool for resolving and displaying Fediverse content. It can parse and display ActivityPub content from various Fediverse platforms including Mastodon, Lemmy, PeerTube, and others.

## Features

- Resolve Fediverse URLs to their ActivityPub representation
- Resolve Fediverse handles (e.g., @username@domain.tld)
- Display both the full JSON data and a human-readable summary
- Support for various ActivityPub types (Person, Note, Article, Create, Announce, etc.)
- Automatic resolution of shared/forwarded content to the original source

## Installation

### Prerequisites

- Go 1.16 or later

### Building from source

```bash
git clone https://github.com/dennis/fediresolve.git
cd fediresolve
go build
```

## Usage

### Basic usage

```bash
# Provide a URL or handle as an argument
./fediresolve https://mastodon.social/@user/12345
./fediresolve @username@domain.tld

# Or run without arguments and enter the URL/handle when prompted
./fediresolve
```

## Examples

### Resolving a Mastodon post

```bash
./fediresolve https://mastodon.social/@Gargron/12345
```

### Resolving a user profile

```bash
./fediresolve @Gargron@mastodon.social
```

## How it works

FediResolve uses the following process to resolve Fediverse content:

1. For handles (@username@domain.tld), it uses the WebFinger protocol to discover the ActivityPub actor URL
2. For URLs, it attempts to fetch the ActivityPub representation directly
3. It checks if the content is shared/forwarded and resolves to the original source if needed
4. It parses the ActivityPub JSON and displays both the raw data and a formatted summary

## License

MIT
