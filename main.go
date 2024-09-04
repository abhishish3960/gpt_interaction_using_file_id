package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Struct to store conversation history
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var conversationHistory []Message

const openAIKey = "your api key" // Replace with your actual OpenAI API key
const maxTokens = 128000         // Model's maximum context length, adjust if needed

func main() {
	r := gin.Default()

	// Endpoint to upload a file
	r.POST("/upload", func(c *gin.Context) {
		// Get the file from the form data
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No file is received"})
			return
		}

		// Open the file
		fileContent, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to open the file"})
			return
		}
		defer fileContent.Close()

		// Upload the file to OpenAI
		fileID, err := uploadFile(file.Filename, fileContent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"file_id": fileID})
	})

	// Chat endpoint
	r.POST("/chat", func(c *gin.Context) {
		var requestBody struct {
			Prompt string `json:"prompt"`
			FileID string `json:"file_id"` // Field to accept file ID
		}

		if err := c.BindJSON(&requestBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Retrieve file content if file_id is provided
		if requestBody.FileID != "" {
			fileContent, err := getFileContent(requestBody.FileID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			// Add file content to the conversation history
			conversationHistory = append(conversationHistory, Message{Role: "user", Content: fileContent})
		}

		// Add the provided prompt to the conversation history
		if requestBody.Prompt != "" {
			// Calculate tokens of the new message
			newMessageTokens := countTokens(requestBody.Prompt)

			// Trim history to fit the new message
			trimHistoryForNewMessage(newMessageTokens)

			conversationHistory = append(conversationHistory, Message{Role: "user", Content: requestBody.Prompt})
		}

		// Get response from ChatGPT based on the conversation history
		response, err := getChatGPTResponse(conversationHistory)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Add assistant message to history
		conversationHistory = append(conversationHistory, Message{Role: "assistant", Content: response})

		c.JSON(http.StatusOK, gin.H{"response": response})
	})

	r.Run(":8080")
}

// Function to upload a file to OpenAI
func uploadFile(filename string, fileContent io.Reader) (string, error) {
	// Create a new multipart writer
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Create a form file field for the file
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		fmt.Println("Error creating form file:", err) // Log error
		return "", err
	}

	// Copy the file content to the form file field
	_, err = io.Copy(part, fileContent)
	if err != nil {
		fmt.Println("Error copying file content:", err) // Log error
		return "", err
	}

	// Add the 'purpose' field
	err = writer.WriteField("purpose", "fine-tune") // Replace "fine-tune" with your intended purpose
	if err != nil {
		fmt.Println("Error adding purpose field:", err) // Log error
		return "", err
	}

	// Close the multipart writer
	writer.Close()

	// Create a new HTTP request
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/files", body)
	if err != nil {
		fmt.Println("Error creating new request:", err) // Log error
		return "", err
	}

	// Set the required headers
	req.Header.Set("Authorization", "Bearer "+openAIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err) // Log error
		return "", err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error response from OpenAI: %s\n", responseBody) // Log the response body
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response body
	var responseBody struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		fmt.Println("Error decoding response body:", err) // Log error
		return "", err
	}

	return responseBody.ID, nil
}

// Function to retrieve file content from OpenAI
func getFileContent(fileID string) (string, error) {
	url := fmt.Sprintf("https://api.openai.com/v1/files/%s/content", fileID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+openAIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// Function to get a response from OpenAI's chat model
func getChatGPTResponse(history []Message) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	url := "https://api.openai.com/v1/chat/completions"

	reqBody := map[string]interface{}{
		"model":    "gpt-4o-mini", // Adjust this based on the available model
		"messages": history,
	}

	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+openAIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error response from OpenAI: %s\n", responseBody) // Log the response body
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var responseBody struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return "", err
	}

	if len(responseBody.Choices) > 0 {
		return responseBody.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("no choices found in response")
}

// Function to trim conversation history to fit within a token limit before adding a new message
func trimHistoryForNewMessage(newMessageTokens int) {
	// Calculate the current token count of the conversation history
	currentTokens := 0
	for _, msg := range conversationHistory {
		currentTokens += countTokens(msg.Content)
	}

	// Check if adding the new message would exceed the token limit
	if currentTokens+newMessageTokens > maxTokens {
		// Remove messages from the start of the history until it fits within the token limit
		for len(conversationHistory) > 0 && currentTokens+newMessageTokens > maxTokens {
			currentTokens -= countTokens(conversationHistory[0].Content)
			conversationHistory = conversationHistory[1:]
		}
	}
}

// Function to count the number of tokens in a given text
func countTokens(text string) int {
	// For simplicity, we assume one token per word; this is a rough estimate.
	// In reality, you might need to use a more accurate tokenizer like GPT-3's to count tokens.
	return len(strings.Fields(text))
}
