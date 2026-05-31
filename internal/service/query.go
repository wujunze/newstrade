package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Article struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	NewsType   string     `json:"news_type"`
	EngineType string     `json:"engine_type"`
	Link       string     `json:"link"`
	AIScore    *int       `json:"ai_score"`
	AIGrade    *string    `json:"ai_grade"`
	AISignal   *string    `json:"ai_signal"`
	Ts         *time.Time `json:"ts"`
	CreatedAt  time.Time  `json:"created_at"`
	Coins      []Coin     `json:"coins"`
}

type Coin struct {
	Symbol     string  `json:"symbol"`
	MarketType *string `json:"market_type"`
	Signal     *string `json:"signal"`
	Grade      *string `json:"grade"`
	Score      *int    `json:"score"`
}

type ListParams struct {
	Page       int
	PageSize   int
	Signal     string
	NewsType   string
	EngineType string
	Symbol     string
	Search     string
}

type ListResult struct {
	Articles []Article `json:"articles"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

func ListArticles(params ListParams) (*ListResult, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}

	where, args := buildWhere(params)
	offset := (params.Page - 1) * params.PageSize

	var total int

	// count query — own timeout so later queries get a full budget
	{
		ctx, cancel := context.WithTimeout(context.Background(), dbOpTimeout)
		defer cancel()
		countQ := `SELECT COUNT(DISTINCT a.id) FROM news_articles a` + joinCoins(params) + where
		if err := db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
			return nil, fmt.Errorf("count: %w", err)
		}
	}

	// data query — DISTINCT prevents duplicate rows when an article has multiple
	// matching coin rows (e.g. BTC-SPOT and BTC-PERP both satisfy symbol=BTC).
	nextArg := len(args) + 1
	dataQ := fmt.Sprintf(`
		SELECT DISTINCT ON (a.created_at, a.id)
		       a.id, a.text, a.news_type, a.engine_type, a.link,
		       a.ai_score, a.ai_grade, a.ai_signal, a.ts, a.created_at
		FROM news_articles a
		%s
		%s
		ORDER BY a.created_at DESC, a.id
		LIMIT $%d OFFSET $%d`,
		joinCoins(params), where, nextArg, nextArg+1)

	ctx, cancel := context.WithTimeout(context.Background(), dbOpTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, dataQ, append(args, params.PageSize, offset)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var articles []Article
	var ids []string
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Text, &a.NewsType, &a.EngineType, &a.Link,
			&a.AIScore, &a.AIGrade, &a.AISignal, &a.Ts, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		a.Coins = []Coin{}
		articles = append(articles, a)
		ids = append(ids, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		if err := attachCoins(ctx, articles, ids); err != nil {
			return nil, err
		}
	}

	if articles == nil {
		articles = []Article{}
	}
	return &ListResult{
		Articles: articles,
		Total:    total,
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

func joinCoins(p ListParams) string {
	if p.Symbol == "" {
		return ""
	}
	return " JOIN news_coins c ON c.news_id = a.id"
}

func buildWhere(p ListParams) (string, []any) {
	var clauses []string
	var args []any

	if p.Signal != "" {
		args = append(args, p.Signal)
		clauses = append(clauses, fmt.Sprintf("a.ai_signal = $%d", len(args)))
	}
	if p.NewsType != "" {
		args = append(args, p.NewsType)
		clauses = append(clauses, fmt.Sprintf("a.news_type = $%d", len(args)))
	}
	if p.EngineType != "" {
		args = append(args, p.EngineType)
		clauses = append(clauses, fmt.Sprintf("a.engine_type = $%d", len(args)))
	}
	if p.Symbol != "" {
		args = append(args, strings.ToUpper(p.Symbol))
		clauses = append(clauses, fmt.Sprintf("c.symbol = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		clauses = append(clauses, fmt.Sprintf("a.text ILIKE $%d", len(args)))
	}

	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func attachCoins(ctx context.Context, articles []Article, ids []string) error {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	q := fmt.Sprintf(`
		SELECT news_id, symbol, market_type, signal, grade, score
		FROM news_coins
		WHERE news_id IN (%s)
		ORDER BY id`,
		strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("coins query: %w", err)
	}
	defer rows.Close()

	idx := make(map[string]int, len(articles))
	for i, a := range articles {
		idx[a.ID] = i
	}

	for rows.Next() {
		var newsID string
		var c Coin
		var mt, sig, gr sql.NullString
		var sc sql.NullInt64
		if err := rows.Scan(&newsID, &c.Symbol, &mt, &sig, &gr, &sc); err != nil {
			return fmt.Errorf("coin scan: %w", err)
		}
		if mt.Valid {
			c.MarketType = &mt.String
		}
		if sig.Valid {
			c.Signal = &sig.String
		}
		if gr.Valid {
			c.Grade = &gr.String
		}
		if sc.Valid {
			n := int(sc.Int64)
			c.Score = &n
		}
		if i, ok := idx[newsID]; ok {
			articles[i].Coins = append(articles[i].Coins, c)
		}
	}
	return rows.Err()
}

// FilterOptions returns distinct values for filter dropdowns.
func FilterOptions() (signals, newsTypes, engineTypes []string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ai_signal FROM news_articles WHERE ai_signal IS NOT NULL ORDER BY ai_signal`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, nil, nil, err
		}
		signals = append(signals, s)
	}

	rows2, err := db.QueryContext(ctx, `
		SELECT DISTINCT news_type FROM news_articles WHERE news_type != '' ORDER BY news_type`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var s string
		if err := rows2.Scan(&s); err != nil {
			return nil, nil, nil, err
		}
		newsTypes = append(newsTypes, s)
	}

	rows3, err := db.QueryContext(ctx, `
		SELECT DISTINCT engine_type FROM news_articles WHERE engine_type != '' ORDER BY engine_type`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows3.Close()
	for rows3.Next() {
		var s string
		if err := rows3.Scan(&s); err != nil {
			return nil, nil, nil, err
		}
		engineTypes = append(engineTypes, s)
	}

	return signals, newsTypes, engineTypes, nil
}
