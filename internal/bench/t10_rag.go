package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexS8332/AnimalGuide_Task22/internal/agent"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/agents"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/embed"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/features"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task22/internal/rag"
)

// Дорожки И-10. Часть A — отвечающий агент без инструментов в двух
// режимах (не диалог); часть B — ведущий в чате с механизмом rag и без
// него. Основная дорожка части B — с rag: с ней стенд сверяет вторую.
const (
	laneAnsNoRAG  = "norag"
	laneAnsRAG    = "rag"
	laneChatRAG   = "чат: с rag"
	laneChatNoRAG = "чат: без rag"
)

// Пороги И-10 (предложение, задание 22).
const (
	ragMinGain        = 3   // rag верен минимум на 3 вопроса чаще norag
	ragMinRecall      = 0.8 // доказательство в выдаче rag на отвечаемых
	ragMaxConfident   = 1   // уверенных ошибок у rag
	ragDefaultRepeats = 2   // rag и rag′ — шум модели
	ragSeed           = 22
)

// DefaultRAGDialog — test-вопросы части B: конфликт с памятью модели (MDD),
// свежий факт, подраздел и синоним — то, где база должна быть видна.
var DefaultRAGDialog = []string{"T03", "T01", "T04", "T07"}

// RAG — И-10: «Ответ с базой знаний и без».
//
// Часть A — основное сравнение, как И-8/И-9 без стенда диалогов: один и тот
// же отвечающий агент без инструментов (rag.Answerer) отвечает на
// контрольные вопросы test в режимах norag и rag, каждый режим дважды
// (второй повтор — замер шума модели), судья-модель (rag.Judge) оценивает
// ответы вперемешку, не зная режима. Жёсткие проверки:
//
//   - rag верен (большинство повторов) минимум на 3 вопроса чаще norag, и
//     разница больше шума — смен вердикта между повторами; меньше шума —
//     «не определено»;
//   - доказательство вопроса в выдаче rag на отвечаемых — ≥ 0.8;
//   - уверенных ошибок у rag — ≤ 1;
//   - на неотвечаемых (T09, T10) rag говорит «не знаю» хотя бы в половине.
//
// Часть B — продуктовое число: ведущий в чате с механизмом rag и без него
// (дорожки отличаются ровно механизмом rag, CheckLanes) на 4 вопросах
// test подряд в одном диалоге. Только отчётные числа: запросов на ход,
// токены, доля кэша, цена и был ли вызов kb_search кодом по журналу.
//
// Базы нет — проверки «не определено» с причиной и подсказкой: база не
// в git, её собирает kb index. Эмбеддер не отвечает — поиск уходит в BM25;
// это не провал, а заметка.
type RAG struct {
	// KBPath — kb.db; пусто — Env.KB (база приложения), иначе kb.db в
	// корне репозитория.
	KBPath string
	// Questions — пусто → DefaultQuestions.
	Questions string
	// Embedder — nil → embed.FromEnv, если сайдкар отвечает; иначе BM25.
	Embedder embed.Embedder
	// Repeats — повторов каждого режима части A; 0 → 2.
	Repeats int
	// Dialog — вопросы части B; nil → DefaultRAGDialog; пустой — без части B.
	Dialog []string
}

// NewRAG — И-10 с настройками по умолчанию.
func NewRAG() *RAG { return &RAG{} }

func (t *RAG) ID() string    { return "И-10" }
func (t *RAG) Title() string { return "Ответ с базой знаний и без" }

func (t *RAG) Run(ctx context.Context, s *Stand, r *Result) error {
	r.Goal = "с базой знаний ответ точнее, чем без неё: верных больше на контрольных вопросах, доказательства находятся, уверенных ошибок нет"
	r.Mechanism = features.RAG
	if s.env.LLM == nil {
		return errors.New("И-10: стенду не передан клиент модели (Env.LLM)")
	}
	r.Lanes = append(r.Lanes,
		LaneInfo{Name: laneAnsNoRAG, Note: "отвечающий агент без инструментов: системный промпт и вопрос", Diff: "не диалог: один запрос к модели"},
		LaneInfo{Name: laneAnsRAG, Note: "тот же агент: правило «опирайся на фрагменты» и найденные фрагменты после вопроса", Diff: "+ фрагменты базы знаний"})

	checks := []string{"rag верен чаще norag (большинство повторов, test)", "доказательство в выдаче rag на отвечаемых",
		"уверенных ошибок у rag", "«не знаю» у rag на вопросах без ответа в базе"}
	path := t.kbPath(s)
	qs, err := kb.LoadQuestions(orDefault(t.Questions, DefaultQuestions))
	if err != nil {
		r.yes("контрольные вопросы читаются", laneAnsRAG, false, err.Error())
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		why := fmt.Sprintf("базы знаний нет: %s — соберите: go run ./cmd/kb index -strategy all", path)
		for _, c := range checks {
			r.pending(c, "—", laneAnsRAG, why)
		}
		return nil
	}
	st, err := kb.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("И-10: база знаний %s: %w", path, err)
	}
	defer st.Close()
	emb := t.Embedder
	if emb == nil {
		h := embed.FromEnv()
		if hs := h.Health(ctx); hs.OK {
			emb = h
		} else {
			r.note("Эмбеддер недоступен (%s): поиск rag — BM25. Это не провал испытания, но recall у BM25 другой.", hs.Why)
		}
	}
	a := &rag.Answerer{LLM: s.env.LLM, Model: s.env.Model, Searcher: &kb.Searcher{Store: st, Embedder: emb}}
	repeats := t.Repeats
	if repeats <= 0 {
		repeats = ragDefaultRepeats
	}
	s.env.logf("  И-10: контрольные вопросы test, norag и rag × %d, судья", repeats)
	rep, err := rag.Eval(ctx, a, qs, rag.EvalOptions{Splits: []string{kb.SplitTest}, Modes: []rag.Mode{rag.NoRAG, rag.RAG},
		Repeats: repeats, Judge: &rag.Judge{LLM: s.env.LLM, Model: s.env.Model}, Seed: ragSeed,
		Progress: func(row rag.Row) {
			s.env.logf("  И-10 %s: norag %s, rag %s", row.Question.ID, orDash(string(row.Majority[rag.NoRAG])), orDash(string(row.Majority[rag.RAG])))
		}})
	if err != nil {
		return fmt.Errorf("И-10: %w", err)
	}
	t.judge(r, rep, checks)
	t.report(r, rep)
	if len(t.dialog()) > 0 {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		return t.chat(ctx, s, r, abs, qs)
	}
	return nil
}

func (t *RAG) kbPath(s *Stand) string {
	if t.KBPath != "" {
		return t.KBPath
	}
	if s.env.KB != "" {
		return s.env.KB
	}
	return DefaultCacheDB
}

func (t *RAG) dialog() []string {
	if t.Dialog == nil {
		return DefaultRAGDialog
	}
	return t.Dialog
}

// judge — жёсткие проверки части A.
func (t *RAG) judge(r *Result, rep rag.Report, checks []string) {
	ra, no := statOf(rep, rag.RAG), statOf(rep, rag.NoRAG)
	d, noise := ra.Correct-no.Correct, ra.Flips+no.Flips
	c := Check{What: checks[0], Want: fmt.Sprintf("≥ +%d вопроса и больше шума", ragMinGain), Lane: laneAnsRAG,
		Got: fmt.Sprintf("rag %d, norag %d из %d (%+d); смен вердикта между повторами: rag %d, norag %d",
			ra.Correct, no.Correct, ra.Questions, d, ra.Flips, no.Flips)}
	switch {
	case d < ragMinGain:
		c.Status = Fail
	case d <= noise:
		c.Status, c.Note = Pending, "разница не больше шума модели — по одному прогону её не засчитать"
	default:
		c.Status = Pass
	}
	r.check(c)

	hit, n := 0, 0
	for _, row := range rep.Rows {
		if !row.Question.Answerable || len(row.Question.Evidence) == 0 {
			continue
		}
		for _, run := range row.Runs[rag.RAG] {
			if run.Error != "" {
				continue
			}
			n++
			if run.Recall {
				hit++
			}
		}
	}
	rc := Check{What: checks[1], Want: fmt.Sprintf("≥ %.2f", ragMinRecall), Lane: laneAnsRAG,
		Got: fmt.Sprintf("%.2f (%d из %d прогонов; индекс %s, k = %d)", ra.Recall, hit, n, rep.Index, rep.K), Status: Pass}
	if n == 0 || ra.Recall+1e-9 < ragMinRecall {
		rc.Status = Fail
	}
	r.check(rc)

	cw := Check{What: checks[2], Want: fmt.Sprintf("≤ %d", ragMaxConfident), Lane: laneAnsRAG, Got: fmt.Sprint(ra.ConfidentWrong), Status: Pass}
	if ra.ConfidentWrong > ragMaxConfident {
		cw.Status = Fail
		var ids []string
		for _, row := range rep.Rows {
			v := row.Majority[rag.RAG]
			if (row.Question.Answerable && v == rag.Wrong) || (!row.Question.Answerable && v != rag.Abstain && v != "") {
				ids = append(ids, row.Question.ID)
			}
		}
		cw.Note = strings.Join(ids, ", ")
	}
	r.check(cw)

	un := 0
	for _, row := range rep.Rows {
		if !row.Question.Answerable {
			un++
		}
	}
	if un == 0 {
		r.pending(checks[3], "—", laneAnsRAG, "в наборе нет вопросов без ответа в базе")
		return
	}
	r.atLeast(checks[3], laneAnsRAG, ra.RightAbstain, un, (un+1)/2)
}

// report — отчётные числа, заметки и ответы части A.
func (t *RAG) report(r *Result, rep rag.Report) {
	r.metric("индекс, k, эмбеддер", "", "%s, %d, %s; corpus_sha %s", rep.Index, rep.K, rep.Embedder, clip(rep.CorpusSHA, 12))
	un := 0
	for _, row := range rep.Rows {
		if !row.Question.Answerable {
			un++
		}
	}
	for _, st := range rep.Stats {
		lane := string(st.Mode)
		r.metric("верно / частично / неверно / «не знаю» (большинство)", lane, "%d / %d / %d / %d из %d", st.Correct, st.Partial, st.Wrong, st.Abstain, st.Questions)
		r.metric("уверенных ошибок", lane, "%d", st.ConfidentWrong)
		r.metric("«не знаю» на вопросах без ответа в базе", lane, "%d из %d", st.RightAbstain, un)
		if st.Mode == rag.RAG {
			r.metric("доказательство в выдаче", lane, "%.2f", st.Recall)
		}
		r.metric("смен вердикта между повторами (шум)", lane, "%d (%.0f %% переходов)", st.Flips, 100*st.FlipRate)
		if st.Judged > 0 {
			r.metric("согласие правила и судьи", lane, "%.0f %% из %d", 100*st.Agreement, st.Judged)
		}
		r.metric("токены ответов (из кэша)", lane, "%d → %d (%d)", st.Usage.Prompt, st.Usage.Completion, st.Usage.CacheHit)
		r.metric("цена ответов", lane, "$%.4f", st.Cost.USD)
		r.metric("среднее время ответа", lane, "%d мс", st.AvgMillis)
	}
	r.metric("цена судьи", "судья", "$%.4f", rep.JudgeCost.USD)
	r.metric("расхождений правила и судьи", "судья", "%d", len(rep.Disagreements))
	for _, c := range rep.Conclusion {
		r.note("%s", c)
	}
	for _, d := range firstN(rep.Disagreements, 5) {
		r.note("Расхождение голосов: %s", d)
	}

	// Пары ответов по трём вопросам: сначала те, где режимы разошлись.
	var picked []rag.Row
	for _, row := range rep.Rows {
		if len(picked) < 3 && row.Majority[rag.RAG] != row.Majority[rag.NoRAG] {
			picked = append(picked, row)
		}
	}
	for _, row := range rep.Rows {
		if len(picked) >= 3 {
			break
		}
		if row.Majority[rag.RAG] == row.Majority[rag.NoRAG] {
			picked = append(picked, row)
		}
	}
	for _, row := range picked {
		q := row.Question
		user := q.Q
		if len(q.Context) > 0 {
			user = strings.Join(q.Context, " → ") + " → " + q.Q
		}
		for _, m := range []rag.Mode{rag.NoRAG, rag.RAG} {
			runs := row.Runs[m]
			if len(runs) == 0 {
				continue
			}
			run := runs[0]
			reply := run.Answer.Text
			if run.Error != "" {
				reply = "(ошибка: " + run.Error + ")"
			}
			note := "итог " + orDash(string(row.Majority[m])) + "; правило " + string(run.Rule.Verdict)
			if run.Judge != nil {
				note += "; судья " + string(run.Judge.Verdict) + ": " + run.Judge.Reason
			}
			r.Samples = append(r.Samples, Sample{Topic: q.ID + " " + q.Type, Lane: string(m), User: user, Reply: reply, Note: note})
		}
	}
}

func statOf(rep rag.Report, m rag.Mode) rag.ModeStats {
	for _, s := range rep.Stats {
		if s.Mode == m {
			return s
		}
	}
	return rag.ModeStats{Mode: m}
}

// ragLanes — дорожки части B: основная с rag, вторая — без.
func ragLanes(base features.Set) []Lane {
	return []Lane{
		{Name: laneChatRAG, Note: "умолчания + rag: kb_search и его вызов кодом с репликой до первого запроса ведущего",
			Features: base.With(features.RAG, true)},
		{Name: laneChatNoRAG, Note: "умолчания без rag: ведущий с Википедией и GBIF", Features: base.With(features.RAG, false)},
	}
}

// laneTurns — ходы дорожки части B.
type laneTurns struct {
	turns, preload, failed, calls int
	tokens, cacheHit, prompt      int
	cost                          float64
	fallback                      string
}

// chat — часть B: ведущий с базой против ведущего с Википедией.
func (t *RAG) chat(ctx context.Context, s *Stand, r *Result, kbPath string, qs kb.QuestionSet) error {
	sub, err := s.Sub("chat", Options{KB: kbPath})
	if err != nil {
		return err
	}
	lanes := ragLanes(s.env.Base)
	var tmp Result
	tmp.describeLanes(s.env.Registry, lanes)
	r.Lanes = append(r.Lanes, tmp.Lanes...)
	g, err := sub.Group("И-10: ведущий с базой и без", lanes)
	if err != nil {
		return err
	}
	byID := map[string]kb.Question{}
	for _, q := range qs.Questions {
		byID[q.ID] = q
	}
	tally := map[string]*laneTurns{}
	for _, l := range lanes {
		tally[l.Name] = &laneTurns{}
	}
	send := func(text string, sample bool, topic string) error {
		steps, err := g.Send(ctx, agents.Request{Text: text})
		if err != nil {
			return err
		}
		for _, st := range steps {
			x := tally[st.Lane]
			x.turns++
			if !st.OK() {
				x.failed++
			}
			x.calls += st.Turn.Totals.LLMCalls
			u := st.Turn.Totals.Usage
			x.tokens += u.Prompt + u.Completion
			x.prompt += u.Prompt
			x.cacheHit += u.CacheHit
			x.cost += st.Turn.Totals.Cost.USD
			for _, e := range st.Turn.Events {
				if e.Kind == agent.EventToolCall && e.Tool == rag.ToolName && strings.Contains(e.Title, "кодом до первого запроса") {
					x.preload++
					break
				}
			}
			if st.Lane == laneChatRAG && !st.Turn.Effective.On(features.RAG) && x.fallback == "" {
				x.fallback = "ход шёл без rag"
				for _, e := range st.Turn.Events {
					if e.Mechanism == string(features.RAG) {
						x.fallback = e.Title
					}
				}
			}
			if sample {
				r.sample(topic, st, "")
			}
		}
		return nil
	}
	// Ответы чата — по первым двум вопросам: отчёт показывает ограниченное
	// число ответов, и пары части A важнее.
	for i, id := range t.dialog() {
		q, ok := byID[id]
		if !ok {
			return fmt.Errorf("И-10: вопроса %s нет в наборе", id)
		}
		for _, c := range q.Context {
			if err := send(c, false, ""); err != nil {
				return err
			}
		}
		if err := send(q.Q, i < 2, "чат "+q.ID+" "+q.Type); err != nil {
			return err
		}
	}
	for _, l := range lanes {
		x := tally[l.Name]
		if x.turns == 0 {
			continue
		}
		n := float64(x.turns)
		r.metric("kb_search кодом до первого запроса (по журналу)", l.Name, "%d из %d ходов", x.preload, x.turns)
		r.metric("запросов к модели на ход", l.Name, "%.1f", float64(x.calls)/n)
		r.metric("токенов на ход", l.Name, "%.0f", float64(x.tokens)/n)
		share := 0.0
		if x.prompt > 0 {
			share = float64(x.cacheHit) / float64(x.prompt)
		}
		r.metric("доля кэша", l.Name, "%.0f %%", 100*share)
		r.metric("цена на ход", l.Name, "$%.4f", x.cost/n)
		if x.failed > 0 {
			r.metric("ходов с ошибкой", l.Name, "%d", x.failed)
		}
		if x.fallback != "" {
			r.note("Чат с rag: механизм откатился — %s.", x.fallback)
		}
	}
	return nil
}
