package main

import (
	"fmt"
	"net/http"
)

func main() {
	fmt.Println("---------------------------------------")
	fmt.Println("WLEDger V2 - Dev Environment Running...")
	fmt.Println("Listening on http://localhost:8080")
	fmt.Println("---------------------------------------")

	// Prevent immediate exit
	http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("WLEDger V2 is Live!"))
	}))
}
