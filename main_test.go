package main

import (
	"encoding/json"
	"reflect"
	"testing"
	// "github.com/stretchr/testify/assert"
)

// PostmanResponse struct
type PostmanResponse struct {
	// Args string `json:"args"` // "args":{},
	// Data string `json:"data"` // "data":{"key":"value"},
	//"headers":{"x-forwarded-proto":"https","host":"postman-echo.com","content-length":"17","accept-encoding":"gzip","content-type":"application/json","user-agent":"Go-http-client/1.1","x-zonduuid":"0ebde02b-e522-40a2-4010-6d26b84f6026","x-forwarded-port":"443"},
	JSON map[string]string `json:"json"` // "json":{"key":"value"},
	// "url":"https://postman-echo.com/post"
}

func TestGet(t *testing.T) {
	tests := map[string]struct {
		url  string
		want string
	}{
		"error":     {url: "http://echo.jsontest.com/error", want: string("{\"error\": \"\"}\n")},
		"ok":        {url: "http://echo.jsontest.com/key/value", want: string("{\"key\": \"value\"}\n")},
		"wrong url": {url: "http://test.example", want: string("error")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := Get(tc.url)
			if !reflect.DeepEqual(tc.want, got) {
				t.Errorf("expected: %v, got: %v", tc.want, got)
			}
		})
	}
}

func TestPost(t *testing.T) {
	tests := map[string]struct {
		url  string
		json string
		want map[string]string
	}{
		"simple":     {url: "https://postman-echo.com/post", json: string("{\"key\": \"value\"}"), want: map[string]string{"key": "value"}},
		"empty json": {url: "https://postman-echo.com/post", json: string("{\"error\"}"), want: make(map[string]string)},
		"error":      {url: "http://test.example", json: "{\"test\": \"test\"}", want: make(map[string]string)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := Post(tc.url, tc.json)
			var got = new(PostmanResponse)
			b := []byte(result)
			err := json.Unmarshal(b, &got)
			if err == nil {
				if len(tc.want) == 0 && len(got.JSON) == 0 {

				} else if !reflect.DeepEqual(tc.want, got.JSON) {
					t.Errorf("expected: %v, got: %v", tc.want, got.JSON)
				}
			}
		})
	}
}

// func TestRestart(t *testing.T) {
// 	// err := Restart()

// 	assert.NoError(t, nil)
// }
