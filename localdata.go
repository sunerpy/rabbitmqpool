package rabbitmqpool

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// const filePath string = "data.txt"

var mutex sync.Mutex

func writeToLocalFile(data string, filePath string) error {
	mutex.Lock()
	defer mutex.Unlock()

	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Failed open file: ", filePath)
		return err
	}
	defer file.Close()
	_, err = file.WriteString(data + "\n")
	if err != nil {
		fmt.Println("Write to file failed: ", err)
		return err
	}
	// encoder := json.NewEncoder(file)
	// if err := encoder.Encode(data); err != nil {
	// 	return err
	// }
	return nil
}

func TmpMain() {
	filePath := "localdata.txt"
	readAndSendData(filePath)
}

func readAndSendData(filePath string) {
	for {
		fmt.Println("Start reading file:")
		time.Sleep(30 * time.Second)

		mutex.Lock()
		data, err := os.ReadFile(filePath)
		if err != nil {
			mutex.Unlock()
			fmt.Println("Error reading file:", err)
			continue
		}
		mutex.Unlock()

		lines := splitLines(string(data))
		for _, line := range lines {
			// Simulate sending data (replace with actual sending logic)
			success := sendData(line)
			if success {
				removeLineFromFile(line, filePath)
				fmt.Println("Data sent successfully and line removed:", line)
			} else {
				fmt.Println("Failed to send data:", line)
			}
		}
	}
}

func splitLines(input string) []string {
	return strings.Split(input, "\n")
}

func sendData(data string) bool {
	// Replace this with actual sending logic
	// If sending is successful, return true; otherwise, return false
	fmt.Println("data content is ", data)
	return true
}

func removeLineFromFile(lineToRemove string, filePath string) {
	mutex.Lock()
	defer mutex.Unlock()

	input, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("Error reading file for removing line:", err)
		return
	}

	lines := splitLines(string(input))
	var output []string
	for _, line := range lines {
		if line != lineToRemove {
			output = append(output, line)
		}
	}

	err = os.WriteFile(filePath, []byte(strings.Join(output, "\n")), 0644)
	if err != nil {
		fmt.Println("Error writing file after removing line:", err)
	}
}

// func testmain() {
// 	// Example usage
// 	err := writeToLocalFile("Data to be written")
// 	if err != nil {
// 		log.Fatal("Error writing to file:", err)
// 	}

// 	// Start reading and sending data from the file in the background
// 	go readAndSendData()

// 	// Keep the program running
// 	select {}
// }
