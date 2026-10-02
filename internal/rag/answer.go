package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task22/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/llm"
)

// answerSystem — системный промпт отвечающего агента, ОБЩИЙ для обоих
// режимов. Режимы отличаются только правилом ragRule в конце и блоком
// фрагментов в сообщении пользователя: всё остальное побайтно одинаково,
// иначе у разницы в ответах была бы вторая причина. Правило «не знаешь —
// скажи» стоит в общей части намеренно: без него norag выдумывал бы чаще,
// чем модель умеет на самом деле, и сравнение мерило бы промпт, а не базу.
const answerSystem = `Ты — справочник о животных. Отвечай по-русски, кратко и по существу: сначала прямой ответ на вопрос, затем, если нужно, одно-два уточнения. Числа называй с единицами измерения.
Если не знаешь ответа или не уверен в нём — так и скажи прямо, не выдумывай факты, числа и названия.`

// ragRule — правило режима RAG. Идёт последним абзацем системного
// промпта: общий префикс обоих режимов кэшируется у провайдера, а правило
// и фрагменты — хвост запроса.
const ragRule = `К вопросу приложены фрагменты базы знаний справочника. Опирайся только на них, а не на свою память: если фрагменты расходятся с тем, что ты помнишь, верны фрагменты. Называй источники — chunk_id фрагментов в квадратных скобках, например [manul/structure/004]. Если во фрагментах ответа нет — так и скажи: «в базе знаний этого нет», и не дополняй ответ по памяти.`

// System — системный промпт режима (для вкладки и тестов).
func System(mode Mode) string {
	if mode == RAG {
		return answerSystem + "\n\n" + ragRule
	}
	return answerSystem
}

// Query — строка поиска по вопросу: предыдущие реплики и сам вопрос через
// пробел (как в kb.Compare). Вопрос-продолжение «а сколько она весит?» без
// предыдущей реплики искать нечем.
func (q Question) Query() string {
	parts := make([]string, 0, len(q.Context)+1)
	for _, c := range q.Context {
		if c = strings.TrimSpace(c); c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(append(parts, strings.TrimSpace(q.Text)), " ")
}

// QuestionOf — вопрос набора для отвечающего агента.
func QuestionOf(q kb.Question) Question {
	return Question{Text: q.Q, Context: q.Context}
}

// Answer отвечает в режиме. RAG без базы (Searcher == nil) — ошибка, а не
// тихий NoRAG.
//
// Один запрос к модели, без инструментов, температура 0. Контекст вопроса
// (предыдущие реплики человека) идёт отдельными сообщениями пользователя
// перед вопросом — так их видит и ведущий в чате; ответов справочника на
// них у отвечающего агента нет, поэтому истории ассистента нет. Фрагменты
// базы — в том же сообщении, что вопрос, после него.
func (a *Answerer) Answer(ctx context.Context, q Question, mode Mode) (Answer, error) {
	if strings.TrimSpace(q.Text) == "" {
		return Answer{}, errors.New("пустой вопрос")
	}
	if mode != NoRAG && mode != RAG {
		return Answer{}, fmt.Errorf("неизвестный режим %q (norag, rag)", mode)
	}
	if a == nil || a.LLM == nil {
		return Answer{}, errors.New("отвечающему агенту не передана модель")
	}
	out := Answer{Mode: mode, System: System(mode), User: strings.TrimSpace(q.Text)}
	if mode == RAG {
		if a.Searcher == nil {
			return Answer{}, errors.New("режим rag без базы знаний: соберите её командой kb index")
		}
		hits, info, err := a.Searcher.Search(ctx, q.Query(), kb.SearchOptions{Index: a.index(), K: a.k()})
		if err != nil {
			return Answer{}, fmt.Errorf("поиск по базе знаний: %w", err)
		}
		out.Hits, out.Search = hits, info
		out.User += "\n\n" + Compose(hits)
	}
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: out.System}}
	for _, c := range q.Context {
		if c = strings.TrimSpace(c); c != "" {
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: c})
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: out.User})

	started := time.Now()
	resp, err := a.LLM.Chat(ctx, llm.Request{Model: a.model(), Messages: msgs, Temperature: 0})
	out.Millis = time.Since(started).Milliseconds()
	if err != nil {
		return Answer{}, fmt.Errorf("модель (%s): %w", mode, err)
	}
	out.Text = strings.TrimSpace(resp.Message.Content)
	out.Usage = resp.Usage
	out.Cost = llm.PriceOf(a.model(), resp.Usage, a.now())
	return out, nil
}

func (a *Answerer) index() string { return orIndex(a.Index) }
func (a *Answerer) k() int        { return orK(a.K) }

func (a *Answerer) model() string {
	if strings.TrimSpace(a.Model) == "" {
		return llm.DefaultModel
	}
	return a.Model
}

func (a *Answerer) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
