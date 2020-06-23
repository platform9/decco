package slack

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
)

func PostBestEffort(url string, msg string, log logr.Logger) {
	err := Post(url, msg)
	if err != nil {
		if log != nil {
			log.Info(err.Error())
		} else {
			fmt.Println(err.Error())
		}
	}
}

func Post(url string, msg string) error {
	body := fmt.Sprintf(`{"text":"%s"}`, msg)
	buf := bytes.NewReader([]byte(body))
	resp, err := http.Post(url, "application/json", buf)
	if err != nil {
		return fmt.Errorf("failed to post to slack: %w", err)
	}
	status := resp.StatusCode
	if status != 200 {
		return fmt.Errorf("unexpected slack status code: %d", status)
	}
	return nil
}
