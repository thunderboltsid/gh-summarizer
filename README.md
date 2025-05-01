# gh-summarizer

A command-line tool that lists all GitHub repositories a user has contributed to.

## Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/gh-summarizer.git
cd gh-summarizer
```

## Build
```bash
make build
```

## Usage
Basic usage
```bash
gh-summarizer --username=thunderboltsid
```

Usage with a custom GitHub token
```bash
gh-summarizer --username=thunderboltsid --token=ghp_123
```

Usage with combined flags
```bash
h-summarizer --username=thunderboltsid --exclude-private-repos --exclude-forks --contributions-since=2024-01-01T00:00:00Z --token=ghp_123 --csv
```
