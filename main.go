package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// Used for JSON parsing from GitHub API
type IssueSearchResult struct {
	Items []struct {
		RepositoryURL string `json:"repository_url"`
		CreatedAt     string `json:"created_at"`
	} `json:"items"`
}

type Repository struct {
	FullName       string
	Private        bool
	Owner          string
	Fork           bool
	LastContribAt  time.Time
	ContribChecked bool
}

var (
	// Command line flags
	username           string
	token              string
	excludePrivate     bool
	excludePublic      bool
	excludeUserOwned   bool
	excludeForks       bool
	contributionsSince string
	version            string = "0.1.0"
)

// Version information - will be set by the build process
var (
	Version   string
	GitCommit string
	BuildDate string
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

			// Parse contributions-since if specified
			var sinceTime time.Time
			var err error
			if contributionsSince != "" {
				sinceTime, err = time.Parse(time.RFC3339, contributionsSince)
				if err != nil {
					fmt.Printf("Error parsing contributions-since timestamp: %v\n", err)
					fmt.Println("Please use RFC3339 format (e.g., '2023-01-01T00:00:00Z')")
					os.Exit(1)
				}
			}

			// Execute the main functionality
			findContributions(sinceTime)
		},
		Version: version,
	}

	// Add flags
	rootCmd.Flags().StringVar(&username, "username", "", "GitHub username (required)")
	rootCmd.Flags().StringVar(&token, "token", "", "GitHub API token (optional, but recommended)")
	rootCmd.Flags().BoolVar(&excludePrivate, "exclude-private-repos", false, "Exclude private repositories")
	rootCmd.Flags().BoolVar(&excludePublic, "exclude-public-repos", false, "Exclude public repositories")
	rootCmd.Flags().BoolVar(&excludeUserOwned, "exclude-user-owned-repos", false, "Exclude repositories owned by the user")
	rootCmd.Flags().BoolVar(&excludeForks, "exclude-forks", false, "Exclude forked repositories")
	rootCmd.Flags().StringVar(&contributionsSince, "contributions-since", "", "Only include repositories with contributions since timestamp (RFC3339 format, e.g., '2023-01-01T00:00:00Z')")

	// Make username required
	rootCmd.MarkFlagRequired("username")

	// Execute the command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// Main function to find all repositories a user has contributed to
func findContributions(sinceTime time.Time) {
	// Create a map to store unique repository information
	repoMap := make(map[string]*Repository)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Check if we need to filter by contribution date
	needsContribDate := !sinceTime.IsZero()

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
		getIssueRepos(username, token, repoMap, &mu, sinceTime, needsContribDate)
	}()

	// Get repositories where user has created pull requests
	wg.Add(1)
	go func() {
		defer wg.Done()
		getPRRepos(username, token, repoMap, &mu, sinceTime, needsContribDate)
	}()

	// Wait for all goroutines to complete
	wg.Wait()

	// Add additional repository information if needed
	if excludePrivate || excludePublic || excludeForks || needsContribDate {
		fetchRepoInfo(repoMap, token, &mu, needsContribDate, sinceTime, username)
	}

	// Print all repositories, applying filters
	printRepos(repoMap, excludePrivate, excludePublic, excludeUserOwned, excludeForks, username, sinceTime)
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

				// Extract owner information
				owner := ""
				ownerInfo, ok := repo["owner"].(map[string]interface{})
				if ok {
					ownerLogin, ok := ownerInfo["login"].(string)
					if ok {
						owner = ownerLogin
					}
				}

				// Extract private status if available
				isPrivate, ok := repo["private"].(bool)

				// Extract fork status
				isFork, ok := repo["fork"].(bool)
				if !ok {
					isFork = false
				}

				// Extract last contribution time (for own repos, use pushed_at)
				var lastContribAt time.Time
				pushedAt, ok := repo["pushed_at"].(string)
				if ok {
					lastContribAt, _ = time.Parse(time.RFC3339, pushedAt)
				}

				mu.Lock()
				if _, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName:       fullName,
						Private:        ok && isPrivate,
						Owner:          owner,
						Fork:           isFork,
						LastContribAt:  lastContribAt,
						ContribChecked: !lastContribAt.IsZero(),
					}
				}
				mu.Unlock()
			}
			page++
		}
	}
}

// Fetch repositories where user has created issues
func getIssueRepos(username, token string, repoMap map[string]*Repository, mu *sync.Mutex, sinceTime time.Time, needsContribDate bool) {
	page := 1
	hasMorePages := true

	// Add since parameter to query if needed
	sinceParam := ""
	if needsContribDate {
		sinceParam = fmt.Sprintf("+created:>=%s", sinceTime.Format(time.RFC3339))
	}

	for hasMorePages {
		url := fmt.Sprintf("https://api.github.com/search/issues?q=author:%s+type:issue%s&page=%d&per_page=100&sort=created&order=desc",
			username, sinceParam, page)
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

				// Parse the creation time
				createdAt, err := time.Parse(time.RFC3339, issue.CreatedAt)
				if err != nil {
					createdAt = time.Time{} // Zero time if parsing fails
				}

				mu.Lock()
				if existingRepo, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName:       fullName,
						Private:        false, // Will be checked later if needed
						Owner:          repoOwner,
						Fork:           false, // Will be checked later if needed
						LastContribAt:  createdAt,
						ContribChecked: !createdAt.IsZero(),
					}
				} else if needsContribDate && createdAt.After(existingRepo.LastContribAt) {
					// Update last contribution time if this is more recent
					existingRepo.LastContribAt = createdAt
					existingRepo.ContribChecked = true
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
func getPRRepos(username, token string, repoMap map[string]*Repository, mu *sync.Mutex, sinceTime time.Time, needsContribDate bool) {
	page := 1
	hasMorePages := true

	// Add since parameter to query if needed
	sinceParam := ""
	if needsContribDate {
		sinceParam = fmt.Sprintf("+created:>=%s", sinceTime.Format(time.RFC3339))
	}

	for hasMorePages {
		url := fmt.Sprintf("https://api.github.com/search/issues?q=author:%s+type:pr%s&page=%d&per_page=100&sort=created&order=desc",
			username, sinceParam, page)
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

				// Parse the creation time
				createdAt, err := time.Parse(time.RFC3339, pr.CreatedAt)
				if err != nil {
					createdAt = time.Time{} // Zero time if parsing fails
				}

				mu.Lock()
				if existingRepo, exists := repoMap[fullName]; !exists {
					repoMap[fullName] = &Repository{
						FullName:       fullName,
						Private:        false, // Will be checked later if needed
						Owner:          repoOwner,
						Fork:           false, // Will be checked later if needed
						LastContribAt:  createdAt,
						ContribChecked: !createdAt.IsZero(),
					}
				} else if needsContribDate && createdAt.After(existingRepo.LastContribAt) {
					// Update last contribution time if this is more recent
					existingRepo.LastContribAt = createdAt
					existingRepo.ContribChecked = true
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

// Fetch additional repository information
func fetchRepoInfo(repoMap map[string]*Repository, token string, mu *sync.Mutex, needsContribDate bool, sinceTime time.Time, username string) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent API requests

	for fullName, repo := range repoMap {
		// Skip repository if we already have all the information we need
		if (repo.Private || (!excludePrivate && !excludePublic)) && // We know visibility status or don't need it
			repo.Fork || !excludeForks && // We know fork status or don't need it
			(repo.ContribChecked || !needsContribDate) { // We know contribution date or don't need it
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore

		go func(fullName string, repo *Repository) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			// Fetch repository details
			url := fmt.Sprintf("https://api.github.com/repos/%s", fullName)
			body, err := fetchGithubAPI(url, token)
			if err != nil {
				return
			}

			var repoData map[string]interface{}
			if err := json.Unmarshal([]byte(body), &repoData); err != nil {
				return
			}

			mu.Lock()
			// Update private status if needed
			if excludePrivate || excludePublic {
				isPrivate, ok := repoData["private"].(bool)
				if ok {
					repo.Private = isPrivate
				}
			}

			// Update fork status if needed
			if excludeForks {
				isFork, ok := repoData["fork"].(bool)
				if ok {
					repo.Fork = isFork
				}
			}
			mu.Unlock()

			// If we need contribution date and don't have it yet, fetch user's contributions
			if needsContribDate && !repo.ContribChecked {
				fetchLastContributionDate(repo, username, token, mu)
			}
		}(fullName, repo)
	}

	wg.Wait()
}

// Fetch the last contribution date for a repository
func fetchLastContributionDate(repo *Repository, username, token string, mu *sync.Mutex) {
	// Try to get the latest PR or issue from this user in this repo
	url := fmt.Sprintf("https://api.github.com/search/issues?q=repo:%s+author:%s&sort=updated&order=desc&per_page=1",
		repo.FullName, username)
	body, err := fetchGithubAPI(url, token)
	if err != nil {
		return
	}

	var searchResult map[string]interface{}
	if err := json.Unmarshal([]byte(body), &searchResult); err != nil {
		return
	}

	items, ok := searchResult["items"].([]interface{})
	if !ok || len(items) == 0 {
		return
	}

	item, ok := items[0].(map[string]interface{})
	if !ok {
		return
	}

	updatedAt, ok := item["updated_at"].(string)
	if ok {
		lastContribTime, err := time.Parse(time.RFC3339, updatedAt)
		if err == nil {
			mu.Lock()
			repo.LastContribAt = lastContribTime
			repo.ContribChecked = true
			mu.Unlock()
		}
	}
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
			resetTimeInt, parseErr := strconv.ParseInt(resetTime, 10, 64)
			if parseErr == nil {
				waitTime := time.Until(time.Unix(resetTimeInt, 0))
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
func printRepos(repoMap map[string]*Repository, excludePrivate, excludePublic, excludeUserOwned, excludeForks bool, username string, sinceTime time.Time) {
	// Convert map to slice for potential sorting
	repos := make([]string, 0, len(repoMap))
	for fullName, repo := range repoMap {
		// Apply visibility filters
		if (excludePrivate && repo.Private) || (excludePublic && !repo.Private) {
			continue
		}

		// Apply user ownership filter
		if excludeUserOwned && strings.EqualFold(repo.Owner, username) {
			continue
		}

		// Apply fork filter
		if excludeForks && repo.Fork {
			continue
		}

		// Apply contribution date filter
		if !sinceTime.IsZero() && repo.ContribChecked && repo.LastContribAt.Before(sinceTime) {
			continue
		}

		repos = append(repos, fullName)
	}

	// Print each repository
	for _, repo := range repos {
		fmt.Println(repo)
	}
}
