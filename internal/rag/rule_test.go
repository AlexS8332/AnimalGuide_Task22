package rag

import (
	"strings"
	"testing"

	"github.com/AlexS8332/AnimalGuide_Task22/internal/kb"
)

// Правило на настоящих вопросах: верные ответы разными словами, частичные,
// неверные, отказы и ложные срабатывания, ради которых сравнение идёт по
// токенам, а не подстрокой.
func TestRuleOnQuestions(t *testing.T) {
	qs := questions(t)
	cases := []struct {
		id, answer string
		want       Verdict
		why        string
	}{
		{"T01", "Банк России 9 августа 2024 года выпустил серебряную монету «Перевязка» номиналом 2 рубля, тираж — 5 тысяч экземпляров [marbled-polecat/structure/012].", Correct, "всё на месте"},
		{"T01", "Серебряную монету номиналом два рубля, тираж 5 000 экземпляров, в 2024 году.", Correct, "«два рубля», «5 000»"},
		{"T01", "Серебряную монету номиналом 2 рубля, в 2024 году.", Partial, "нет тиража"},
		{"T01", "Золотую монету в 2010 году.", Wrong, "мимо"},
		{"T02", "Взрослый самец съедает 45–54 г пищи в сутки — это три-четыре мыши.", Correct, "диапазон через тире"},
		{"T02", "Около 1945 года отмечали 540 грамм в сутки.", Wrong, "45 не в 1945, 54 не в 540"},
		{"T02", "Не знаю точно.", Abstain, "отказ"},
		{"T03", "По MDD v2.5 — два вида: Ailurus fulgens и Ailurus styani (Eastern Red Panda) [mdd-carnivora/structure/003].", Correct, "конфликт решён по базе"},
		{"T03", "MDD признаёт один вид малой панды — Ailurus fulgens с двумя подвидами.", Wrong, "память модели; «двумя подвидами» — не «два вида»"},
		{"T04", "Погоня обычно около 500 метров, скорость — до 50 км/ч.", Correct, ""},
		{"T04", "Погоня длится около 5 км со скоростью 500 км/ч.", Wrong, "числа есть, но не в тех формах"},
		{"T05", "Дольше у каракала: 78–81 день против примерно 66 дней у камышового кота — на 12–15 дней.", Correct, ""},
		{"T07", "Малая панда ест по 13 часов в день [red-panda/structure/013].", Correct, ""},
		{"T07", "Тринадцать часов в сутки.", Correct, "число словами"},
		{"T07", "Малая панда тратит на еду большую часть суток [red-panda/structure/013].", Wrong, "13 — только в chunk_id"},
		{"T08", "Самцы харзы весят 2,5–5,8 кг, самки — 1,1–3,8 кг.", Correct, "запятая"},
		{"T08", "Самцы 2.5-5.8 кг, самки 1.1-3.8 кг.", Correct, "точка"},
		{"T08", "Самцы 2,5–5,8 кг.", Partial, "половина"},
		{"T09", "В базе знаний нет данных о продолжительности жизни камышового кота.", Abstain, "верный отказ"},
		{"T09", "Данных в базе нет, но обычно 13–15 лет.", Wrong, "must_not «15 лет» в диапазоне"},
		{"T09", "В природе около 14 лет, в неволе дольше.", Wrong, "уверенно по существу"},
		{"T10", "Не знаю: о фоссе в базе знаний сведений нет.", Abstain, ""},
		{"T10", "Взрослая фосса весит 7–12 кг.", Wrong, "ответ по существу без базы"},
		{"D16", "Около 58 тысяч особей, крупнейшая популяция — в Монголии.", Correct, "тысячи"},
		{"D16", "Около 58 тыс. особей.", Correct, "«тыс.»"},
		{"D08", "CR — на грани исчезновения.", Correct, ""},
		{"D08", "Статус EN — вымирающий.", Wrong, "must_not EN"},
		{"D06", "От 370 до 730 г.", Correct, ""},
		{"D12", "38 хромосом.", Correct, ""},
		{"D12", "380 хромосом.", Wrong, "38 не в 380"},
		{"D05", "До одиннадцати месяцев: яйцеклетка сначала «отдыхает» и не сразу начинает развиваться.", Correct, ""},
	}
	for _, c := range cases {
		q := question(t, qs, c.id)
		got := Rule(q, c.answer)
		if got.Verdict != c.want {
			t.Errorf("%s %q (%s): %s, ждали %s; %+v", c.id, c.answer, c.why, got.Verdict, c.want, got)
		}
	}
}

func TestRuleDetails(t *testing.T) {
	qs := questions(t)
	r := Rule(question(t, qs, "T01"), "Серебряную монету номиналом 2 рубля, в 2024 году.")
	if strings.Join(r.Hit, ",") != "2 рубля,серебрян" || strings.Join(r.Miss, ",") != "5 тысяч" ||
		strings.Join(r.Numbers, ",") != "2024 ✓,2 ✓,5000 ✗" || !strings.Contains(r.Note, "групп 2 из 3, чисел 2 из 3") {
		t.Fatalf("разбор: %+v", r)
	}
	r = Rule(question(t, qs, "T09"), "Данных нет, но обычно 13–15 лет.")
	if strings.Join(r.Bad, ",") != "15 лет" {
		t.Fatalf("must_not: %+v", r)
	}
	if r := Rule(question(t, qs, "T10"), "Не знаю."); !strings.Contains(r.Note, "не знаю") {
		t.Fatalf("причина отказа: %+v", r)
	}
}

// Числа: допуск, диапазон с множителем, «2,5» = «2.5», отдельные числа не
// склеиваются в разряды.
func TestRuleNumbers(t *testing.T) {
	q := kb.Question{Answerable: true, Expect: &kb.Expect{Numbers: []kb.Number{{V: 2000}, {V: 3000}}}}
	if v := Rule(q, "Около 2–3 тыс. особей").Verdict; v != Correct {
		t.Fatalf("диапазон с множителем: %s", v)
	}
	q.Expect.Numbers = []kb.Number{{V: 100, Tol: 5}}
	if Rule(q, "примерно 104 см").Verdict != Correct || Rule(q, "примерно 106 см").Verdict == Correct {
		t.Fatal("допуск")
	}
	q.Expect.Numbers = []kb.Number{{V: 1.5}}
	if Rule(q, "весит 1,5 кг").Verdict != Correct || Rule(q, "весит полтора килограмма").Verdict != Correct {
		t.Fatal("1,5 и полтора")
	}
	q.Expect.Numbers = []kb.Number{{V: 2024}, {V: 500}}
	if Rule(q, "в 2024 500 особей").Verdict != Correct {
		t.Fatal("«2024 500» не должно склеиться")
	}
	q.Expect.Numbers = []kb.Number{{V: 58000}}
	if Rule(q, "около 58 000").Verdict != Correct || Rule(q, "около 58").Verdict == Correct {
		t.Fatal("разряды")
	}
	// Без ожидания правило не судит, но отказ видит.
	empty := kb.Question{Answerable: true}
	if Rule(empty, "Ответ.").Verdict != Partial || Rule(empty, "Не знаю.").Verdict != Abstain {
		t.Fatal("вопрос без ожидания")
	}
}

func TestTokenMatch(t *testing.T) {
	p := parse("Масса 5 г; LC — вызывающий наименьшие опасения; 13 часов; в 2024 году.")
	for form, want := range map[string]bool{
		"г": true, "грамм": false, "LC": true, "наименьшие опасения": true, "13 час": true, "13 ч": false,
		"2024": true, "202": false, "года": false, "году": true, "EN": false,
	} {
		if p.has(form) != want {
			t.Errorf("%q: %v", form, !want)
		}
	}
	// «г» целиком: в «года» не находится.
	if parse("в 2024 году").has("г") {
		t.Fatal("«г» в «году»")
	}
}
