package main

import (
	"fmt"
	"net/http"
)

const version = "v0"

func main() {
	fmt.Println("air-app starting", version)
	http.ListenAndServe(":8080", nil)
}
