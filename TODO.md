# TODO — упразднён → GitHub Issues

Баги, задачи и tech-debt теперь ведутся в **GitHub Issues**:
**https://github.com/PRO-Robotech/kacho-vpc/issues**

- То, что упирается в ещё-не-реализованные сервисы — метки `blocked:kacho-dns` / `blocked:kacho-iam`
  (в теле issue — при каких условиях браться).
- Конвенция (метки, кросс-репо зависимости, что НЕ заводить как issue) — `../../CLAUDE.md`
  («Баги, задачи, tech-debt — GitHub Issues» и §14.4 в `kacho-vpc/CLAUDE.md`).
- Кросс-репо порядок выполнения / merge — `../../CLAUDE.md` → «Кросс-репо зависимости и порядок выполнения».
- By-design расхождения с verbatim-YC (это **не** баги) — `docs/architecture/07-known-divergences.md`.
- Что раньше было в этом файле (закрытые пункты, FINDING-ы, исправленные баги) — `git log -- TODO.md`.

> Этот файл оставлен как маяк на случай, если кто-то по старой памяти откроет `TODO.md`.
