package types

import "time"

type Folder struct {
	Name        string `json:"name"`
	Delimiter   string `json:"delimiter"`
	MessageCount uint32 `json:"message_count"`
	Unread      uint32 `json:"unread"`
}

type Message struct {
	UID       uint32    `json:"uid"`
	Folder    string    `json:"folder"`
	Subject   string    `json:"subject"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Date      time.Time `json:"date"`
	Unread    bool      `json:"unread"`
	Flagged   bool      `json:"flagged"`
	BodyText  string    `json:"body_text,omitempty"`
}

type Config struct {
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	Email    string `json:"email"`
}

// Wire types for the REST API.

type StatusResponse struct {
	Syncing bool     `json:"syncing"`
	Folders []Folder `json:"folders"`
}

type MessagesResponse struct {
	Folder   string    `json:"folder"`
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
}

type MessageResponse struct {
	Message Message `json:"message"`
}
