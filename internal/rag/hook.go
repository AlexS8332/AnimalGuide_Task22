package rag

import (
	"context"

	"github.com/AlexS8332/AnimalGuide_Task22/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/runs"
)

// HookName — имя хука в журнале хода.
const HookName = "rag"

// Hook — механизм rag (features.RAG): ведущий получает kb_search, а код
// вызывает его до первого запроса с репликой человека (agents.Request.
// Preload), плюс абзац правил (Request.Rules): опирайся на найденные
// фрагменты и называй их [chunk_id]; если фрагменты не отвечают на
// вопрос — так и скажи и при необходимости иди в Википедию/GBIF.
//
// Базы нет (Searcher == nil) — механизм откатывается: ход идёт без
// kb_search, в журнале причина и подсказка, а в наборе механизмов хода rag
// выключен (как у trivia). Эмбеддер не отвечает — не откат механизма:
// поиск сам уходит в BM25 и называет причину в ответе инструмента.
//
// Голые названия («манул») и «сравни» идут мимо ведущего (agents.Classify) —
// база там не участвует; это предмет задания 25.
type Hook struct {
	Searcher *kb.Searcher
	Index    string // пусто — DefaultIndex
	K        int    // 0 — DefaultK
	Why      string // почему базы нет (для журнала)
}

func (h *Hook) Name() string { return HookName }

func (h *Hook) Before(ctx context.Context, t *runs.Turn) error { return nil }

func (h *Hook) After(ctx context.Context, t *runs.Turn) error { return nil }

var _ runs.Hook = (*Hook)(nil)
