package main

import (
	"fmt"
	"net/http"
)

// session counts
var counts = map[string]int{}

func main() {
	// increment session count
	http.HandleFunc("/incr", func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session_id")
		counts[session] += 1
		fmt.Fprintf(w, ": session=%v, count=%v\n", session, counts[session])
	})

	// start server
	fmt.Println(http.ListenAndServe(":8080", nil))
}
