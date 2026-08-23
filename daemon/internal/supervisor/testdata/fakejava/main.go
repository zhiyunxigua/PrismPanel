package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	fmt.Println("fake server ready")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := scanner.Text()
		fmt.Printf("command: %s\n", command)
		if command == "stop" {
			fmt.Println("fake server stopped")
			return
		}
	}
}
