package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
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
	}

	txtRecords, err := net.LookupTXT(domain)
	if err == nil {
		for _, record := range txtRecords {
			if strings.HasPrefix(record, "v=spf1") {
				result.HasSPF = true
				break
			}
		}
	}

	dmarcRecords, err := net.LookupTXT("_dmarc." + domain)
	if err == nil {
		for _, record := range dmarcRecords {
			if strings.HasPrefix(record, "v=DMARC1") {
				result.HasDMARC = true
				break
			}
		}
	}

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

func processCSV(inputFile, outputFile string) error {
	// Open the input file
	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a new CSV reader
	reader := csv.NewReader(file)

	// Read all records
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	// Find the index of the "email" column
	var emailIndex int
	for i, header := range records[0] {
		if strings.ToLower(header) == "email" {
			emailIndex = i
			break
		}
	}

	// Add a new header for the safety status
	records[0] = append(records[0], "send_status")

	// Process each record
	for i := 1; i < len(records); i++ {
		email := records[i][emailIndex]
		if isEmailValid(email) {
			records[i] = append(records[i], "safe to send")
		} else {
			records[i] = append(records[i], "unsafe")
		}
	}

	// Create the output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Create a new CSV writer
	writer := csv.NewWriter(outFile)

	// Write all records to the output file
	err = writer.WriteAll(records)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	inputFile := "contacts.csv"
	outputFile := "output.csv"

	err := processCSV(inputFile, outputFile)
	if err != nil {
		log.Fatalf("Error processing CSV: %v", err)
	}

	fmt.Println("CSV processing completed. Results written to", outputFile)
}
