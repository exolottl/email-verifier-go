package main

import (
	"bufio"
	"encoding/csv"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tealeg/xlsx"
	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
)

type DomainCheck struct {
	HasMX    bool
	HasSPF   bool
	HasDMARC bool
}

var (
	dnsCache     = make(map[string]DomainCheck)
	dnsCacheMutex sync.RWMutex
	dnsResolver   *net.Resolver
	sem           *semaphore.Weighted
	verifiedCount int64
	totalEmails   int64
)

func init() {
	dnsResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: time.Second * 10,
			}
			return d.DialContext(ctx, network, address)
		},
	}
	sem = semaphore.NewWeighted(100) // Limit concurrent DNS lookups
}

func extractDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func checkDomain(domain string) DomainCheck {
	dnsCacheMutex.RLock()
	if result, ok := dnsCache[domain]; ok {
		dnsCacheMutex.RUnlock()
		return result
	}
	dnsCacheMutex.RUnlock()

	var result DomainCheck
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		sem.Acquire(context.Background(), 1)
		defer sem.Release(1)
		mxRecords, err := dnsResolver.LookupMX(context.Background(), domain)
		if err == nil {
			result.HasMX = len(mxRecords) > 0
		}
	}()

	go func() {
		defer wg.Done()
		sem.Acquire(context.Background(), 1)
		defer sem.Release(1)
		txtRecords, err := dnsResolver.LookupTXT(context.Background(), domain)
		if err == nil {
			for _, record := range txtRecords {
				if strings.HasPrefix(record, "v=spf1") {
					result.HasSPF = true
					break
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		sem.Acquire(context.Background(), 1)
		defer sem.Release(1)
		dmarcRecords, err := dnsResolver.LookupTXT(context.Background(), "_dmarc."+domain)
		if err == nil {
			for _, record := range dmarcRecords {
				if strings.HasPrefix(record, "v=DMARC1") {
					result.HasDMARC = true
					break
				}
			}
		}
	}()

	wg.Wait()

	dnsCacheMutex.Lock()
	dnsCache[domain] = result
	dnsCacheMutex.Unlock()

	return result
}

func isEmailValid(email string) bool {
	domain := extractDomain(email)
	if domain == "" {
		return false
	}

	result := checkDomain(domain)
	return result.HasMX && result.HasSPF && result.HasDMARC
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
		
		// Log progress every 1000 emails
		if atomic.LoadInt64(&totalEmails)%1000 == 0 {
			log.Printf("Progress: %d emails processed, %d verified", atomic.LoadInt64(&totalEmails), atomic.LoadInt64(&verifiedCount))
		}
	}
	resultChan <- chunk
}

func processRecords(records [][]string, outputFile string, chunkSize int) error {
	var emailIndex int
	for i, header := range records[0] {
		if strings.ToLower(header) == "email" {
			emailIndex = i
			break
		}
	}

	records[0] = append(records[0], "send_status")

	resultChan := make(chan [][]string, 10)
	var wg sync.WaitGroup

	totalEmails = 0
	verifiedCount = 0

	for i := 1; i < len(records); i += chunkSize {
		end := i + chunkSize
		if end > len(records) {
			end = len(records)
		}

		chunk := records[i:end]
		wg.Add(1)
		go func(chunk [][]string) {
			defer wg.Done()
			processChunk(chunk, emailIndex, resultChan)
		}(chunk)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	outFile, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	writer := csv.NewWriter(bufio.NewWriter(outFile))
	defer writer.Flush()

	writer.Write(records[0])

	for chunk := range resultChan {
		writer.WriteAll(chunk)
	}

	return nil
}

func processCSV(inputFile, outputFile string, chunkSize int) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(bufio.NewReader(file))
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	return processRecords(records, outputFile, chunkSize)
}

func processXLSX(inputFile, outputFile string, chunkSize int) error {
	xlFile, err := xlsx.OpenFile(inputFile)
	if err != nil {
		return err
	}

	var records [][]string

	for _, sheet := range xlFile.Sheets {
		for _, row := range sheet.Rows {
			var record []string
			for _, cell := range row.Cells {
				record = append(record, cell.String())
			}
			records = append(records, record)
		}
		break
	}

	return processRecords(records, outputFile, chunkSize)
}

func main() {
	inputFile := "6-8Lakh-Online-shoppers.xlsx"
	outputFile := strings.Split(inputFile, ".")[0] + "_verified.csv"
	chunkSize := 1000

	log.Println("Starting processing...")

	start := time.Now()

	var err error
	if strings.HasSuffix(inputFile, ".csv") {
		err = processCSV(inputFile, outputFile, chunkSize)
	} else if strings.HasSuffix(inputFile, ".xlsx") {
		err = processXLSX(inputFile, outputFile, chunkSize)
	} else {
		log.Fatalf("Unsupported file format")
	}

	if err != nil {
		log.Fatalf("Error processing file: %v", err)
	}

	elapsed := time.Since(start)
	log.Printf("Processing completed in %s. Results written to %s", elapsed, outputFile)
	log.Printf("Final count: %d emails processed, %d verified", atomic.LoadInt64(&totalEmails), atomic.LoadInt64(&verifiedCount))
}