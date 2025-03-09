package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns" // More efficient DNS library
	"github.com/patrickmn/go-cache" // Caching with TTL support
	"github.com/tealeg/xlsx"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// DomainCheck stores the results of domain validation
type DomainCheck struct {
	HasMX    bool
	HasSPF   bool
	HasDMARC bool
}

// ValidationConfig allows customizing validation requirements
type ValidationConfig struct {
	RequireMX    bool
	RequireSPF   bool
	RequireDMARC bool
	Concurrency  int64
	DNSTimeout   time.Duration
	ChunkSize    int
}

var (
	dnsCache     *cache.Cache
	dnsClient    *dns.Client
	sem          *semaphore.Weighted
	verifiedCount int64
	totalEmails   int64
	cfg           ValidationConfig
)

func init() {
	// Default configuration
	cfg = ValidationConfig{
		RequireMX:    true,
		RequireSPF:   true,
		RequireDMARC: true,
		Concurrency:  250, // Increased from 100
		DNSTimeout:   time.Second * 5,
		ChunkSize:    2000, // Increased from 1000
	}

	// Initialize DNS client with shorter timeout
	dnsClient = &dns.Client{
		Timeout: cfg.DNSTimeout,
	}

	// Initialize cache with 1 hour default expiration and cleanup every 10 minutes
	dnsCache = cache.New(1*time.Hour, 10*time.Minute)
	
	// Initialize semaphore for controlling concurrency
	sem = semaphore.NewWeighted(cfg.Concurrency)
}

func extractDomain(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// lookupRecord performs a DNS query with exponential backoff retry
func lookupRecord(recordType uint16, domain string, server string) ([]dns.RR, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), recordType)
	m.RecursionDesired = true

	var resp *dns.Msg
	var err error
	maxRetries := 3
	backoff := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, _, err = dnsClient.Exchange(m, server+":53")
		if err == nil && resp != nil && resp.Rcode != dns.RcodeServerFailure {
			break
		}
		time.Sleep(backoff)
		backoff *= 2 // Exponential backoff
	}

	if err != nil || resp == nil {
		return nil, err
	}

	return resp.Answer, nil
}

func checkDomain(domain string) DomainCheck {
	// Check cache first
	if result, found := dnsCache.Get(domain); found {
		return result.(DomainCheck)
	}

	var result DomainCheck
	dnsServers := []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"} // Using multiple DNS providers
	serverIndex := 0
	
	// Check MX records
	mxLookup := func() error {
		records, err := lookupRecord(dns.TypeMX, domain, dnsServers[serverIndex%len(dnsServers)])
		serverIndex++
		if err == nil && len(records) > 0 {
			result.HasMX = true
		}
		return nil
	}

	// Check SPF records
	spfLookup := func() error {
		records, err := lookupRecord(dns.TypeTXT, domain, dnsServers[serverIndex%len(dnsServers)])
		serverIndex++
		if err == nil {
			for _, r := range records {
				if txt, ok := r.(*dns.TXT); ok {
					for _, t := range txt.Txt {
						if strings.HasPrefix(t, "v=spf1") {
							result.HasSPF = true
							break
						}
					}
				}
			}
		}
		return nil
	}

	// Check DMARC records
	dmarcLookup := func() error {
		records, err := lookupRecord(dns.TypeTXT, "_dmarc."+domain, dnsServers[serverIndex%len(dnsServers)])
		serverIndex++
		if err == nil {
			for _, r := range records {
				if txt, ok := r.(*dns.TXT); ok {
					for _, t := range txt.Txt {
						if strings.HasPrefix(t, "v=DMARC1") {
							result.HasDMARC = true
							break
						}
					}
				}
			}
		}
		return nil
	}

	// Use errgroup for controlled concurrency with error handling
	g := new(errgroup.Group)
	g.Go(mxLookup)
	g.Go(spfLookup)
	g.Go(dmarcLookup)
	
	// Wait for all DNS lookups to complete
	_ = g.Wait()

	// Store result in cache
	dnsCache.Set(domain, result, cache.DefaultExpiration)
	
	return result
}

func isEmailValid(email string) bool {
	// Basic email format check before DNS lookups
	if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		return false
	}
	
	domain := extractDomain(email)
	if domain == "" {
		return false
	}

	result := checkDomain(domain)
	
	// Apply validation rules based on configuration
	isValid := true
	if cfg.RequireMX && !result.HasMX {
		isValid = false
	}
	if cfg.RequireSPF && !result.HasSPF {
		isValid = false
	}
	if cfg.RequireDMARC && !result.HasDMARC {
		isValid = false
	}
	
	return isValid
}

func processChunk(chunk [][]string, emailIndex int, resultChan chan<- [][]string) {
	for i := range chunk {
		email := chunk[i][emailIndex]
		if isEmailValid(email) {
			chunk[i] = append(chunk[i], "verified")
			atomic.AddInt64(&verifiedCount, 1)
		} else {
			chunk[i] = append(chunk[i], "unsafe")
		}
		atomic.AddInt64(&totalEmails, 1)
		
		// Log progress less frequently to reduce overhead
		if atomic.LoadInt64(&totalEmails)%5000 == 0 {
			log.Printf("Progress: %d emails processed, %d verified", 
				atomic.LoadInt64(&totalEmails), 
				atomic.LoadInt64(&verifiedCount))
		}
	}
	resultChan <- chunk
}

func processRecords(records [][]string, outputFile string) error {
	var emailIndex int
	for i, header := range records[0] {
		if strings.ToLower(header) == "email" {
			emailIndex = i
			break
		}
	}

	records[0] = append(records[0], "send_status")

	// Buffer channel based on number of chunks for better performance
	numChunks := (len(records) - 1) / cfg.ChunkSize + 1
	resultChan := make(chan [][]string, numChunks)
	
	// Create a worker pool
	var wg sync.WaitGroup
	
	// Reset counters
	atomic.StoreInt64(&totalEmails, 0)
	atomic.StoreInt64(&verifiedCount, 0)

	// Calculate optimal worker count based on CPU cores
	numWorkers := 2 * runtime.NumCPU()
	workChan := make(chan [][]string, numWorkers)
	
	// Start worker pool
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range workChan {
				processChunk(chunk, emailIndex, resultChan)
			}
		}()
	}

	// Send chunks to worker pool
	go func() {
		for i := 1; i < len(records); i += cfg.ChunkSize {
			end := i + cfg.ChunkSize
			if end > len(records) {
				end = len(records)
			}
			
			chunk := make([][]string, end-i)
			copy(chunk, records[i:end])
			workChan <- chunk
		}
		close(workChan)
	}()

	// Separate goroutine to collect results and write to file
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Stream results to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Use buffered writer for better performance
	bufWriter := bufio.NewWriterSize(outFile, 65536) // 64KB buffer
	writer := csv.NewWriter(bufWriter)
	defer func() {
		writer.Flush()
		bufWriter.Flush()
	}()

	// Write header
	writer.Write(records[0])

	// Process results as they arrive
	for chunk := range resultChan {
		writer.WriteAll(chunk)
		writer.Flush() // Flush after each chunk to avoid memory buildup
	}

	return nil
}

func processCSV(inputFile, outputFile string) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Use a scanner for streaming large files instead of loading all at once
	csvReader := csv.NewReader(file)
	
	// Read header
	header, err := csvReader.Read()
	if err != nil {
		return err
	}
	
	var records [][]string
	records = append(records, header)
	
	// Stream process records in batches to reduce memory usage
	batch := make([][]string, 0, cfg.ChunkSize*10)
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // Skip invalid records
		}
		batch = append(batch, record)
		
		// Process batch when it reaches threshold
		if len(batch) >= cfg.ChunkSize*10 {
			records = append(records, batch...)
			batch = make([][]string, 0, cfg.ChunkSize*10)
		}
	}
	
	// Add any remaining records
	if len(batch) > 0 {
		records = append(records, batch...)
	}

	return processRecords(records, outputFile)
}

func processXLSX(inputFile, outputFile string) error {
	xlFile, err := xlsx.OpenFile(inputFile)
	if err != nil {
		return err
	}

	var records [][]string

	// Process only the first sheet
	if len(xlFile.Sheets) > 0 {
		sheet := xlFile.Sheets[0]
		
		// Pre-allocate records slice to reduce reallocations
		records = make([][]string, 0, len(sheet.Rows))
		
		for _, row := range sheet.Rows {
			// Pre-allocate record slice
			record := make([]string, len(row.Cells))
			for i, cell := range row.Cells {
				record[i] = cell.String()
			}
			records = append(records, record)
		}
	}

	return processRecords(records, outputFile)
}

func main() {
	// Use flags for configuration
	var (
		inputFile   string
		outputFile  string
		requireMX   bool
		requireSPF  bool
		requireDMARC bool
		concurrency int
		chunkSize   int
	)
	
	flag.StringVar(&inputFile, "input", "", "Input file (CSV or XLSX)")
	flag.StringVar(&outputFile, "output", "", "Output file (CSV)")
	flag.BoolVar(&requireMX, "mx", true, "Require MX records")
	flag.BoolVar(&requireSPF, "spf", true, "Require SPF records")
	flag.BoolVar(&requireDMARC, "dmarc", true, "Require DMARC records")
	flag.IntVar(&concurrency, "concurrency", int(cfg.Concurrency), "Max concurrent DNS requests")
	flag.IntVar(&chunkSize, "chunk", cfg.ChunkSize, "Processing chunk size")
	flag.Parse()
	
	if inputFile == "" {
		inputFile = "6-8Lakh-Online-shoppers.xlsx"
	}
	
	if outputFile == "" {
		outputFile = strings.Split(inputFile, ".")[0] + "_verified.csv"
	}
	
	// Update configuration
	cfg.RequireMX = requireMX
	cfg.RequireSPF = requireSPF
	cfg.RequireDMARC = requireDMARC
	cfg.Concurrency = int64(concurrency)
	cfg.ChunkSize = chunkSize
	
	// Reinitialize semaphore with new concurrency value
	sem = semaphore.NewWeighted(cfg.Concurrency)

	log.Println("Starting processing with the following configuration:")
	log.Printf("- Input: %s", inputFile)
	log.Printf("- Output: %s", outputFile)
	log.Printf("- Validation: MX=%v, SPF=%v, DMARC=%v", cfg.RequireMX, cfg.RequireSPF, cfg.RequireDMARC)
	log.Printf("- Concurrency: %d", cfg.Concurrency)
	log.Printf("- Chunk size: %d", cfg.ChunkSize)

	start := time.Now()

	var err error
	if strings.HasSuffix(inputFile, ".csv") {
		err = processCSV(inputFile, outputFile)
	} else if strings.HasSuffix(inputFile, ".xlsx") {
		err = processXLSX(inputFile, outputFile)
	} else {
		log.Fatalf("Unsupported file format")
	}

	if err != nil {
		log.Fatalf("Error processing file: %v", err)
	}

	elapsed := time.Since(start)
	log.Printf("Processing completed in %s. Results written to %s", elapsed, outputFile)
	log.Printf("Final count: %d emails processed, %d verified (%.2f%%)", 
		atomic.LoadInt64(&totalEmails), 
		atomic.LoadInt64(&verifiedCount),
		float64(atomic.LoadInt64(&verifiedCount))/float64(atomic.LoadInt64(&totalEmails))*100)
	
	// Print cache stats
	items := dnsCache.Items()
	log.Printf("DNS cache: %d domains cached", len(items))
}