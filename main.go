package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	ug "github.com/Pilfer/ultimate-guitar-scraper/pkg/ultimateguitar"
)

// ---- Response shapes sent to the watch ----

type SearchResultItem struct {
	ID     int64   `json:"id"`
	Title  string  `json:"title"`
	Artist string  `json:"artist"`
	Type   string  `json:"type"` // "Chords", "Tab", etc.
	Rating float64 `json:"rating"`
	Votes  int64   `json:"votes"` // number of user reviews this tab has received
}

type SearchResponse struct {
	Results []SearchResultItem `json:"results"`
}

type TabResponse struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Key    string `json:"key"`
	Capo   int    `json:"capo"`
	Lines  []Line `json:"lines"`
}

// ---- Simple in-memory cache so we don't hammer UG on every request ----

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	cacheTTL = 12 * time.Hour
)

func getCached(key string) ([]byte, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

func setCached(key string, data []byte) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[key] = cacheEntry{data: data, expiresAt: time.Now().Add(cacheTTL)}
}

// ---- Scraper instance (one per process; the auth header is time-based, not per-request) ----

var scraper = ug.New()

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONCached marshals v, caches the bytes under key, and writes the response.
func writeJSONCached(w http.ResponseWriter, key string, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	setCached(key, data)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing ?q="})
		return
	}

	cacheKey := "search:" + strings.ToLower(q)
	if cached, ok := getCached(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cached)
		return
	}

	result, err := scraper.Search(ug.SearchParams{
		Title: q,
		Type:  []ug.TabType{ug.TabTypeChords, ug.TabTypeUkulele}, // chord charts + ukulele-specific submissions
		Page:  1,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "search failed: " + err.Error()})
		return
	}

	items := make([]SearchResultItem, 0, len(result.Tabs))
	for _, t := range result.Tabs {
		items = append(items, SearchResultItem{
			ID:     t.ID,
			Title:  t.SongName,
			Artist: string(t.ArtistName),
			Type:   string(t.Type),
			Rating: t.Rating,
			Votes:  t.Votes,
		})
	}

	// Most-reviewed first — a tab with far more reviews is generally more
	// trustworthy than one with a high rating from just a handful of votes.
	sort.Slice(items, func(i, j int) bool {
		return items[i].Votes > items[j].Votes
	})

	writeJSONCached(w, cacheKey, SearchResponse{Results: items})
}

func handleTab(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/tab/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tab id"})
		return
	}

	cacheKey := "tab:" + idStr
	if cached, ok := getCached(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cached)
		return
	}

	tab, err := scraper.GetTabByID(id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "fetch failed: " + err.Error()})
		return
	}

	resp := TabResponse{
		ID:     int64(tab.ID),
		Title:  tab.SongName,
		Artist: tab.ArtistName,
		Key:    tab.TonalityName,
		Capo:   tab.Capo,
		Lines:  ParseTabContent(tab.Content),
	}

	writeJSONCached(w, cacheKey, resp)
}

// Bump this string every time you deploy, so /version lets you confirm
// Cloud Run is actually serving the build you just pushed — not a stale
// revision. Cheaper than guessing based on symptoms after every redeploy.
const buildMarker = "parser-v17-preserve-asterisk-annotations"

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"build": buildMarker})
}

func handleSearchRaw(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing ?q="})
		return
	}

	result, err := scraper.Search(ug.SearchParams{Title: q, Page: 1})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "search failed: " + err.Error()})
		return
	}

	// Untouched struct straight from the scraper library — no filtering,
	// no mapping to our own SearchResultItem shape. If this is also
	// empty, the problem is upstream (UG's API itself, or how we're
	// calling it) rather than anything in our own processing logic.
	writeJSON(w, http.StatusOK, result)
}

func handleTabRaw(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/tab-raw/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tab id"})
		return
	}

	tab, err := scraper.GetTabByID(id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "fetch failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(tab.Content))
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/tab/", handleTab)
	mux.HandleFunc("/tab-raw/", handleTabRaw)      // debug: untouched raw content, no parsing applied
	mux.HandleFunc("/search-raw", handleSearchRaw) // debug: untouched scraper.Search() result
	mux.HandleFunc("/version", handleVersion)      // debug: confirm which build is actually deployed

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // local dev fallback; Cloud Run always sets PORT itself
	}
	addr := ":" + port
	log.Printf("ug-chords-backend listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
