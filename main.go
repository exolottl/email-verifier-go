package main

import (
	"bufio"
	"encoding/csv"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tealeg/xlsx"
)

type DomainCheck struct {
	HasMX    bool
	HasSPF   bool
	HasDMARC bool
}

func extractDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func checkDomain(domain string) DomainCheck {
	var result DomainCheck

	mxRecords, err := net.LookupMX(domain)
	if err == nil {
		result.HasMX = len(mxRecords) > 0
	} else {
		log.Printf("Error checking MX records for domain %s: %v", domain, err)
	}

	txtRecords, err := net.LookupTXT(domain)
	if err == nil {
		for _, record := range txtRecords {
			if strings.HasPrefix(record, "v=spf1") {
				result.HasSPF = true
				break
			}
		}
	} else {
		log.Printf("Error checking SPF records for domain %s: %v", domain, err)
	}

	dmarcRecords, err := net.LookupTXT("_dmarc." + domain)
	if err == nil {
		for _, record := range dmarcRecords {
			if strings.HasPrefix(record, "v=DMARC1") {
				result.HasDMARC = true
				break
			}
		}
	} else {
		log.Printf("Error checking DMARC records for domain %s: %v", domain, err)
	}

	return result
}

func isEmailValid(email string) bool {
	domain := extractDomain(email)
	if domain == "" {
		log.Printf("Invalid email format: %s", email)
		return false
	}

	result := checkDomain(domain)
	isValid := result.HasMX && result.HasSPF && result.HasDMARC

	log.Printf("Email: %s, Valid: %t (MX: %t, SPF: %t, DMARC: %t)", email, isValid, result.HasMX, result.HasSPF, result.HasDMARC)
	return isValid
}

func processChunk(chunk [][]string, emailIndex int, chunkNum int, wg *sync.WaitGroup, resultChan chan<- [][]string) {
	defer wg.Done()

	start := time.Now() // Start measuring time for this chunk
	log.Printf("Processing chunk %d with %d records", chunkNum, len(chunk))

	for i := 0; i < len(chunk); i++ {
		email := chunk[i][emailIndex]
		if isEmailValid(email) {
			chunk[i] = append(chunk[i], "verified")
			log.Printf("Email %s verified", email)
		} else {
			chunk[i] = append(chunk[i], "unsafe")
			log.Printf("Email %s marked unsafe", email)
		}
	}

	// Measure the time taken to process this chunk
	elapsed := time.Since(start)
	log.Printf("Finished processing chunk %d, took %v", chunkNum, elapsed)

	resultChan <- chunk
}

func processCSV(inputFile, outputFile string, chunkSize int) error {

	// Open the input file
	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a new CSV reader
	bufferedReader := bufio.NewReader(file)
	reader := csv.NewReader(bufferedReader)

	// Read all records
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

	// Assuming data is in the first sheet
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

func processRecords(records [][]string, outputFile string, chunkSize int) error {
	// Find the index of the "email" column
	var emailIndex int
	for i, header := range records[0] {
		if strings.ToLower(header) == "email" {
			emailIndex = i
			break
		}
	}

	log.Printf("Email column found at index %d", emailIndex)

	// Add a new header for the safety status
	records[0] = append(records[0], "send_status")

	// Create a channel to receive processed chunks
	resultChan := make(chan [][]string, 10)
	var wg sync.WaitGroup

	// Process records in chunks
	for i := 1; i < len(records); i += chunkSize {
		end := i + chunkSize
		if end > len(records) {
			end = len(records)
		}

		chunk := records[i:end]
		wg.Add(1)
		chunkNum := i / chunkSize
		go processChunk(chunk, emailIndex, chunkNum, &wg, resultChan)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Create the output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Create a new CSV writer
	writer := csv.NewWriter(outFile)
	defer writer.Flush()

	// Write the header first
	writer.Write(records[0])

	// Write each processed chunk
	for chunk := range resultChan {
		for _, record := range chunk {
			writer.Write(record)
		}
	}

	// Measure the total time taken to process the file
	elapsed := time.Since(time.Now())
	log.Printf("Total processing time: %v", elapsed)

	return nil
}

func main() {
	inputFile := "6-8Lakh-Online-shoppers.xlsx" // Can be .csv or .xlsx
	outputFile := strings.Split(inputFile, ".")[0] + "_verified.csv"
	chunkSize := 500 // You can adjust the chunk size based on your needs

	log.Println("Starting processing...")

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

	log.Println("Processing completed. Results written to", outputFile)
}
