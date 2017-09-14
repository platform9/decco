package main

import (
	"fmt"
	"net/http"
	"log"
	"os"
	"strconv"
)

var namespace = os.Getenv("POD_NAMESPACE")

func handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(404)
	fmt.Fprintf(w, "Wrong URL path. This is the default handler for %s\n",
		namespace)
}

func main() {
	if namespace == "" {
		namespace = "(unknown)"
	}

	port, err := strconv.Atoi(os.Getenv("LISTEN_PORT"))
	if err != nil {
		log.Printf("failed to decode listen port: %s", err)
		log.Printf("defaulting to 8081")
		port = 8081
	}
	log.Printf("default http server for namespace %s listening on %d",
		namespace, port)
	http.HandleFunc("/", handler)
	err = http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		log.Fatalf("listen failed: %s", err)
	}
}



