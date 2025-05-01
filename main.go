package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// Used for JSON parsing from GitHub API
type IssueSearchResult struct {
	Items []struct {
		RepositoryURL string `json:"repository_url"`
	} `json:"items"`
}

type Repository struct {
	FullName string
	Private  bool
}

var (
	// Command line flags
	username       string
	token          string
	excludePrivate bool
	excludePublic  bool
)

func main() {
	// Root command
	rootCmd := &cobra.Command{
		Use:   "gh-contribution-summarizer",
		Short: "List all GitHub repositories a user has contributed to",
		Long: `A simple command-line utility that retrieves all repositories
a GitHub user has contributed to, including:
- Repositories owned by the user
- Repositories where the user has created issues
- Repositories where the user has submitted pull requests`,
		Run: func(cmd *cobra.Command, args []string) {
			// Validate inputs
			if username == "" {
				fmt.Println("Error: username is required")
				cmd.Help()
				os.Exit(1)
			}

			if (excludePrivate || excludePublic) && token == "" {
				fmt.Println("Warning: GitHub token is required to check repository visibility (private/public).")
				fmt.Println("Without a token, all repositories will be included regardless of visibility flags.")
			}

			// Execute the main functionality
			findContributions()
		},
	}

	// Add flags
	rootCmd.Flags().StringVar(&username, "username", "", "GitHub username (required)")
	rootCmd.Flags().StringVar(&token, "token", "", "GitHub API token (optional, but recommended)")
	rootCmd.Flags().BoolVar(&excludePrivate, "exclude-private-repos", false, "Exclude private repositories")
	rootCmd.Flags().BoolVar(&excludePublic, "exclude-public-repos", false, "Exclude public repositories")

	// Make username required
	rootCmd.MarkFlagRequired("username")

	// Execute the command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// Main function to find all repositories a user has contributed to
func findContributions() {
	// Create a map to store unique repository information
	repoMap := make(map[string]*Repository)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Get user's own repositories
	wg.Add(1)
	go func() {
		defer wg.Done()
		getUserRepos(username, token, repoMap, &mu)
	}()

	// Get repositories where user has created issues
	wg.Add(1)
	go func() {
		defer wg.Done()
		getIssueRepos(username, token, repoMap, &mu)
	}()

	// Get repositories where user has created pull requests
	wg.Add(1)
	go func() {
		defer wg.Done()
		getPRRepos(username, token, repoMap, &mu)
	}()

	// Wait for all goroutines to complete
	wg.Wait()

	// Add visibility information to repositories that need it
	if (excludePrivate || excludePublic) && token != "" {
		fetchRepoVisibility(repoMap, token, &mu)
	}

	// Print all repositories, applying filters
	printRepos(repoMap, excludePrivate, excludePublic)
}

// Fetch user's own repositories
func getUserRepos(username, token string, repoMap map[string]*Repository, mu *sync.Mutex) {
	page := 1
	hasMorePages := true

	for hasMorePages {
		url := fmt.Sprintf("https://api.github.com/users/%s/repos?page=%d&per_page=100", username, page)
		body, err := fetchGithubAPI(url, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching user repos page %d: %v\n", page, err)
			return
		}

		var repos []map[string]interface{}
		if err := json.Unmarshal([]byte(body), &repos); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			return
		}

		if len(repos) == 0 {
			hasMorePages = false
		} else {
			for _, repo := range repos {
				fullName, ok := repo["full_name"].(string)
				if !ok {
					continue
				}

				// Extract private status if available
				isPrivate, ok := repo["private"].(bool)

				mu.Lock()
				if _, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName: fullName,
						Private:  ok && isPrivate,
					}
				}
				mu.Unlock()
			}
			page++
		}
	}
}

// Fetch repositories where user has created issues
func getIssueRepos(username, token string, repoMap map[string]*Repository, mu *sync.Mutex) {
	page := 1
	hasMorePages := true

	for hasMorePages {
		url := fmt.Sprintf("https://api.github.com/search/issues?q=author:%s+type:issue&page=%d&per_page=100", username, page)
		body, err := fetchGithubAPI(url, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching issue repos page %d: %v\n", page, err)
			return
		}

		var searchResult IssueSearchResult
		if err := json.Unmarshal([]byte(body), &searchResult); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			return
		}

		if len(searchResult.Items) == 0 {
			hasMorePages = false
		} else {
			for _, issue := range searchResult.Items {
				// Extract repo info from repository URL
				// Format: https://api.github.com/repos/OWNER/REPO
				urlParts := strings.Split(issue.RepositoryURL, "/")
				if len(urlParts) < 5 {
					continue
				}

				repoOwner := urlParts[4]
				repoName := urlParts[5]
				fullName := repoOwner + "/" + repoName

				mu.Lock()
				if _, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName: fullName,
						Private:  false, // Will be checked later if needed
					}
				}
				mu.Unlock()
			}

			if len(searchResult.Items) < 100 {
				hasMorePages = false
			} else {
				page++
			}
		}
	}
}

// Fetch repositories where user has created pull requests
func getPRRepos(username, token string, repoMap map[string]*Repository, mu *sync.Mutex) {
	page := 1
	hasMorePages := true

	for hasMorePages {
		url := fmt.Sprintf("https://api.github.com/search/issues?q=author:%s+type:pr&page=%d&per_page=100", username, page)
		body, err := fetchGithubAPI(url, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching PR repos page %d: %v\n", page, err)
			return
		}

		var searchResult IssueSearchResult
		if err := json.Unmarshal([]byte(body), &searchResult); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			return
		}

		if len(searchResult.Items) == 0 {
			hasMorePages = false
		} else {
			for _, pr := range searchResult.Items {
				// Extract repo info from repository URL
				urlParts := strings.Split(pr.RepositoryURL, "/")
				if len(urlParts) < 5 {
					continue
				}

				repoOwner := urlParts[4]
				repoName := urlParts[5]
				fullName := repoOwner + "/" + repoName

				mu.Lock()
				if _, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName: fullName,
						Private:  false, // Will be checked later if needed
					}
				}
				mu.Unlock()
			}

			if len(searchResult.Items) < 100 {
				hasMorePages = false
			} else {
				page++
			}
		}
	}
}

// Fetch visibility information for repositories
func fetchRepoVisibility(repoMap map[string]*Repository, token string, mu *sync.Mutex) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent API requests

	for fullName, repo := range repoMap {
		// Skip repositories that already have verified visibility information
		if repo.Private {
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore

		go func(fullName string, repo *Repository) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			url := fmt.Sprintf("https://api.github.com/repos/%s", fullName)
			body, err := fetchGithubAPI(url, token)
			if err != nil {
				return
			}

			var repoData map[string]interface{}
			if err := json.Unmarshal([]byte(body), &repoData); err != nil {
				return
			}

			isPrivate, ok := repoData["private"].(bool)
			if ok {
				mu.Lock()
				repo.Private = isPrivate
				mu.Unlock()
			}
		}(fullName, repo)
	}

	wg.Wait()
}

// Helper function to fetch from GitHub API
func fetchGithubAPI(url, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	// Add a small delay to avoid rate limiting
	time.Sleep(500 * time.Millisecond)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check for rate limiting
	if resp.StatusCode == 403 {
		rateLimitRemaining := resp.Header.Get("X-RateLimit-Remaining")
		if rateLimitRemaining == "0" {
			resetTime := resp.Header.Get("X-RateLimit-Reset")
			resetTimeInt, err := time.Parse(time.RFC3339, resetTime)
			if err == nil {
				waitTime := time.Until(resetTimeInt)
				fmt.Fprintf(os.Stderr, "Rate limit exceeded. Waiting for %v...\n", waitTime)
				time.Sleep(waitTime)
				return fetchGithubAPI(url, token) // Retry after waiting
			}
		}
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// Print repositories, applying filters
func printRepos(repoMap map[string]*Repository, excludePrivate, excludePublic bool) {
	// Convert map to slice for potential sorting
	repos := make([]string, 0, len(repoMap))
	for fullName, repo := range repoMap {
		// Apply visibility filters
		if (excludePrivate && repo.Private) || (excludePublic && !repo.Private) {
			continue
		}
		repos = append(repos, fullName)
	}

	// Print each repository
	for _, repo := range repos {
		fmt.Println(repo)
	}
}
