# 📧 Email Verification Tool

![Email Verification Banner](https://ik.imagekit.io/iquid/tr:w-1000,h-500/email-verifier-high-resolution-logo.png?updatedAt=1741602427247)

## 📋 Contents

- [Overview](#overview)
- [Key Features](#key-features)
- [Getting Started](#getting-started)
- [How to Use](#how-to-use)
- [Understanding the Results](#understanding-the-results)
- [Behind the Scenes](#behind-the-scenes)
- [Tips for Best Results](#tips-for-best-results)
- [Troubleshooting](#troubleshooting)
- [Technical Details](#technical-details)

## Overview

**What does this tool do?** 

This tool checks your email lists to make sure the email addresses are likely to be valid. It does this by checking if the domain part of the email (the part after @) has the proper DNS records that real email servers should have.

**Why is this useful?**

- ✅ **Reduce Bounce Rates** - Remove invalid emails before sending campaigns
- ✅ **Improve Deliverability** - Better sender reputation from fewer bounces
- ✅ **Clean Data** - Identify potentially risky or disposable email addresses
- ✅ **Save Money** - Many email marketing platforms charge by list size

## Key Features

### 🚀 Designed for Performance

- **Lightning Fast**: Processes thousands of emails per minute
- **Handles Large Lists**: Works with files containing hundreds of thousands of emails
- **Smart Caching**: Remembers results to avoid checking the same domain twice

### 🔍 Thorough Verification

Checks three critical DNS records that proper email domains should have:

1. **MX Records** - Tells if a domain can receive email
2. **SPF Records** - Helps prevent email spoofing
3. **DMARC Records** - Provides guidelines for email handling

### 🧰 Flexible & Easy to Use

- **Multiple File Formats**: Works with both CSV and Excel files
- **Customizable Checks**: Choose which DNS checks to perform
- **Simple Output**: Creates an easy-to-understand CSV with results

## Getting Started

### 📥 Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/email-verify.git
cd email-verify

# Build the tool
go build -o email-verify
```

### 📋 Requirements

- Go 1.22 or later
- Internet connection for DNS lookups

## How to Use

### Basic Command

```bash
./email-verify -input your-email-list.csv -output verified-emails.csv
```

### All Available Options

| Option | What it Does | Default Setting |
|--------|--------------|-----------------|
| `-input` | Your file containing emails (CSV or XLSX) | `6-8Lakh-Online-shoppers.xlsx` |
| `-output` | Where to save results (CSV) | `[input-name]_verified.csv` |
| `-mx` | Check for MX records | `true` |
| `-spf` | Check for SPF records | `true` |
| `-dmarc` | Check for DMARC records | `true` |
| `-concurrency` | How many checks to run at once | `250` |
| `-chunk` | How many emails to process at a time | `2000` |

### 📝 Examples

#### Basic Verification

```bash
./email-verify -input customers.xlsx
```

#### Custom Rules (MX only)

```bash
./email-verify -input newsletter-list.csv -spf=false -dmarc=false
```

#### Performance Boost (for large lists)

```bash
./email-verify -input huge-list.csv -concurrency=500 -chunk=5000
```

## Understanding the Results

The tool creates a CSV file with all your original data plus a new column:

| Column | What it Means |
|--------|---------------|
| `send_status` | Either "verified" or "unsafe" |

- **verified** = Domain has all the required DNS records
- **unsafe** = Domain is missing one or more required records

> **Note:** This doesn't guarantee an email exists, just that the domain is properly set up to receive email.

## Behind the Scenes

Here's how the tool works:

1. **Reads Your File**: Opens your CSV or Excel file
2. **Extracts Domains**: Gets the domain from each email address
3. **Checks DNS Records**: Looks up MX, SPF, and DMARC records
4. **Makes a Decision**: Marks each email as "verified" or "unsafe"
5. **Saves Results**: Creates a new CSV with the results

## Tips for Best Results

- **Start Small**: Test with a small list first to understand the results
- **Adjust Requirements**: Not all legitimate domains use all three record types
- **Run During Off-Hours**: DNS lookups are faster when networks are less busy
- **Look for Patterns**: Check if specific domains are consistently marked unsafe

## Troubleshooting

| Problem | Try This |
|---------|----------|
| **Too Slow** | Reduce the list size or increase concurrency |
| **"Connection refused"** | Lower concurrency to avoid DNS rate limiting |
| **High memory usage** | Reduce chunk size |
| **File format errors** | Make sure CSV files use standard formatting |

## Technical Details

### 🛠️ How It's Built

- **Go Language**: Efficient and concurrent by design
- **Worker Pools**: Processes multiple chunks simultaneously
- **DNS Optimization**: 
  - Uses multiple DNS providers (Google, Cloudflare, Quad9)
  - Implements retry logic with exponential backoff
  - Caches results to avoid redundant lookups

### 📦 Dependencies

- `github.com/miekg/dns`: Advanced DNS functionality
- `github.com/patrickmn/go-cache`: In-memory caching
- `github.com/tealeg/xlsx`: Excel file processing
- `golang.org/x/sync`: Concurrency control

### ⚙️ Advanced Configuration

Internal settings can be adjusted via command-line flags:

```go
ValidationConfig{
    RequireMX:    true,     // Check for MX records
    RequireSPF:   true,     // Check for SPF records
    RequireDMARC: true,     // Check for DMARC records
    Concurrency:  250,      // Simultaneous DNS lookups
    DNSTimeout:   5 seconds, // Time before considering lookup failed
    ChunkSize:    2000,     // Emails processed in each batch
}
```

### 🔮 Future Improvements

- Email syntax validation
- Disposable email domain detection
- API for integration with other systems
- More output formats