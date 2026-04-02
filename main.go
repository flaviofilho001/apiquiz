package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// ─── DB ───────────────────────────────────────────────────────────────────────

var db *sql.DB

func initDB() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL não definida")
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Erro ao abrir conexão com banco: %v", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err = db.Ping(); err != nil {
		log.Fatalf("Erro ao conectar no banco: %v", err)
	}

	log.Println("Conectado ao PostgreSQL com sucesso!")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Structs ──────────────────────────────────────────────────────────────────

type Player struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Score struct {
	ID       int       `json:"id"`
	Score    int       `json:"score"`
	Mode     string    `json:"mode"`
	PlayedAt time.Time `json:"played_at"`
}

type RankingEntry struct {
	Name     string    `json:"name"`
	Score    int       `json:"score"`
	Mode     string    `json:"mode"`
	PlayedAt time.Time `json:"played_at"`
}

type Question struct {
	ID            int             `json:"id"`
	Text          string          `json:"text"`
	Options       json.RawMessage `json:"options"`
	CorrectOption int             `json:"correct_option"`
	Difficulty    string          `json:"difficulty"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// GET /
func handleRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Hello world!")
}

// GET /api/health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /api/players
// Body: { "name": "Flávio Mesquita" }
func handleCreatePlayer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Nome é obrigatório."})
		return
	}

	// Verifica duplicata
	var exists int
	err := db.QueryRow(`SELECT COUNT(*) FROM players WHERE LOWER(name) = LOWER($1)`, name).Scan(&exists)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao verificar nome"})
		return
	}
	if exists > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Esse nome já está em uso."})
		return
	}

	var player Player
	err = db.QueryRow(
		`INSERT INTO players (name) VALUES ($1) RETURNING id, name, created_at`,
		name,
	).Scan(&player.ID, &player.Name, &player.CreatedAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao criar jogador"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"player": player})
}

// POST /api/scores
// Body: { "name": "Flávio Mesquita", "score": 100, "mode": "easy" | "medium" | "hard" }
func handleCreateScore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Score int    `json:"score"`
		Mode  string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" || body.Mode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, score e mode são obrigatórios."})
		return
	}

	validModes := map[string]bool{"easy": true, "medium": true, "hard": true}
	if !validModes[body.Mode] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode deve ser easy, medium ou hard."})
		return
	}

	var playerID int
	err := db.QueryRow(`SELECT id FROM players WHERE LOWER(name) = LOWER($1) LIMIT 1`, name).Scan(&playerID)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Jogador não encontrado. Crie o jogador primeiro."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao buscar jogador"})
		return
	}

	var entry Score
	err = db.QueryRow(
		`INSERT INTO scores (player_id, score, mode) VALUES ($1, $2, $3) RETURNING id, score, mode, played_at`,
		playerID, body.Score, body.Mode,
	).Scan(&entry.ID, &entry.Score, &entry.Mode, &entry.PlayedAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao salvar pontuação"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"message": "Pontuação salva!", "entry": entry})
}

// GET /api/ranking?mode=easy&limit=20
func handleRanking(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	validModes := map[string]bool{"easy": true, "medium": true, "hard": true}
	if mode != "" && !validModes[mode] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode deve ser easy, medium ou hard."})
		return
	}

	var rows *sql.Rows
	var err error

	if mode != "" {
		rows, err = db.Query(`
			SELECT p.name, s.score, s.mode, s.played_at
			FROM scores s
			JOIN players p ON p.id = s.player_id
			WHERE s.mode = $1
			ORDER BY s.score DESC
			LIMIT $2
		`, mode, limit)
	} else {
		rows, err = db.Query(`
			SELECT p.name, s.score, s.mode, s.played_at
			FROM scores s
			JOIN players p ON p.id = s.player_id
			ORDER BY s.score DESC
			LIMIT $1
		`, limit)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao buscar ranking"})
		return
	}
	defer rows.Close()

	ranking := []RankingEntry{}
	for rows.Next() {
		var e RankingEntry
		if err := rows.Scan(&e.Name, &e.Score, &e.Mode, &e.PlayedAt); err != nil {
			continue
		}
		ranking = append(ranking, e)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ranking": ranking})
}

// GET /api/questions?mode=easy&total=10
// mode:  easy (todas fáceis) | medium (metade+metade) | hard (todas difíceis)
// total: 5 | 10 | 15
func handleQuestions(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	totalStr := r.URL.Query().Get("total")

	validModes := map[string]bool{"easy": true, "medium": true, "hard": true}
	if !validModes[mode] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode deve ser easy, medium ou hard."})
		return
	}

	total := 10
	if totalStr != "" {
		if v, err := strconv.Atoi(totalStr); err == nil {
			total = v
		}
	}
	validTotals := map[int]bool{5: true, 10: true, 15: true}
	if !validTotals[total] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "total deve ser 5, 10 ou 15."})
		return
	}

	var questions []Question
	var err error

	switch mode {
	case "easy":
		questions, err = fetchQuestions("easy", total)
	case "hard":
		questions, err = fetchQuestions("hard", total)
	case "medium":
		half := total / 2
		rest := total - half
		easyQ, e1 := fetchQuestions("easy", half)
		hardQ, e2 := fetchQuestions("hard", rest)
		if e1 != nil || e2 != nil {
			err = fmt.Errorf("erro ao buscar questões")
		} else {
			questions = append(easyQ, hardQ...)
			// Embaralha
			rand.Shuffle(len(questions), func(i, j int) {
				questions[i], questions[j] = questions[j], questions[i]
			})
		}
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao buscar questões"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":      mode,
		"total":     len(questions),
		"questions": questions,
	})
}

func fetchQuestions(difficulty string, limit int) ([]Question, error) {
	rows, err := db.Query(`
		SELECT id, text, options, correct_option, difficulty
		FROM questions
		WHERE difficulty = $1
		ORDER BY RANDOM()
		LIMIT $2
	`, difficulty, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var questions []Question
	for rows.Next() {
		var q Question
		if err := rows.Scan(&q.ID, &q.Text, &q.Options, &q.CorrectOption, &q.Difficulty); err != nil {
			continue
		}
		questions = append(questions, q)
	}
	return questions, nil
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	initDB()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleRoot)
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("POST /api/players", handleCreatePlayer)
	mux.HandleFunc("POST /api/scores", handleCreateScore)
	mux.HandleFunc("GET /api/ranking", handleRanking)
	mux.HandleFunc("GET /api/questions", handleQuestions)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	handler := corsMiddleware(mux)

	log.Printf("Servidor rodando na porta %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Erro ao iniciar servidor: %v", err)
	}

	//log de flavo
}
