# Estrategia y posicionamiento de `doctier`

> Documento vivo. Fija la **propuesta de valor** y las **decisiones de roadmap** de `doctier`.
> Se apoya en una investigación de mercado (julio 2026) sobre cómo el ecosistema gestiona los
> documentos de contexto generados por agentes de IA. El espacio se mueve en cuestión de meses:
> **revisar este mapa periódicamente** (§6). Para el diseño técnico, ver [`DESIGN.md`](DESIGN.md).

## 1. Propuesta de valor

**`doctier` es la capa de _tiers_ que le falta a AGENTS.md: visibilidad × duración para los
documentos generados, sobre git, nativa en worktrees, cifrada con age y agnóstica del arnés.**

Git modela un solo eje con dos estados (rastreado / ignorado). `doctier` añade los **dos ejes
que los flujos con agentes necesitan de verdad** y que ninguna convención actual tiene:

- **Visibilidad** — `public` (texto plano) · `private` (cifrado con age, reutilizando claves SSH).
- **Duración** — `durable` (para siempre) · `ephemeral` (vida finita, recolección automática).

Con **fail-closed** (imposible publicar un privado en claro por accidente) y **worktrees como
caso de primera clase** (agentes en paralelo, sin hooks de sembrado).

## 2. El problema y hacia dónde va la conversación

Los flujos con agentes generan PRDs, investigación, mapas de decisiones, planes y memoria. Hoy
acaban en git "porque no hay dónde más ponerlos", o gitignored en bloque (nada se respalda ni
viaja). El dolor está bien articulado en los hilos de **[@mattpocockuk](https://x.com/mattpocockuk/status/2069698109492343101)**
y **[@dexhorthy](https://x.com/dexhorthy/status/2069143768901791934)** (junio 2026).

**Hallazgo clave de la investigación (y decisión de fondo de `doctier`):** el hilo pedía
almacenamiento _fuera_ de git con historial lineal. Pero **el ecosistema serio ya convergió en lo
contrario — git como sustrato** — y ahí es donde `doctier` juega:

- **Letta Context Repositories** — memoria de agente respaldada por git, clonada al filesystem,
  con cada subagente en un **worktree aislado** y merge por git. ([letta.com](https://www.letta.com/blog/context-repositories/))
- **Spec Kitty** — specs/planes/tareas versionados en el repo, agentes en paralelo en
  **worktrees**. ([github](https://github.com/Priivacy-ai/spec-kitty))
- **Taxonomía de Martin Fowler** (spec-driven development) — las herramientas ya se segmentan por
  **ciclo de vida**: spec-first (desechable), spec-anchored (persiste), spec-as-source. El eje
  "duración" es real y ya divide el mercado. ([martinfowler.com](https://martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html))

Es decir: **la apuesta "encima de git, no fuera" está alineada con el consenso emergente.** El hilo
describe el dolor; el ecosistema ya decidió el sustrato.

Dos matices que validan los ejes de `doctier`:

- **El plan es efímero por diseño.** El workflow de Pocock externaliza el plan a ficheros PRD/issue
  _para poder tirarlos_, trata el PRD como "marcador de referencia, no input del compilador", y
  asume _doc rot_ (un agente posterior lee un plan viejo, lo cree autoritativo y genera basura). Hoy
  se gestiona a mano (cerrar issues, `/clear`). El eje **efímero + GC** de `doctier` automatiza esa
  decisión humana de descartar.
- **Las convenciones de descubrimiento ganaron, pero son evergreen y sin control de acceso.**
  AGENTS.md es el estándar cross-tool (20k+ repos; Codex, Claude, Cursor, Copilot, Gemini; ahora
  bajo la Linux Foundation) con precedencia jerárquica por path; `CLAUDE.md` se auto-carga y
  fusiona. Pero **ninguna convención tiene eje de duración ni de visibilidad**: el único control es
  gitignore, y "no están pensadas para secretos". Este es el hueco central. ([agents.md](https://agents.md), [code.claude.com](https://code.claude.com/docs/en/memory))

## 3. Mapa competitivo — cada uno posee un eje, nadie el cruce

| Jugador | Eje que posee | Qué le falta vs `doctier` |
|---|---|---|
| **Letta** | memoria git-backed + worktrees | sin tiers de visibilidad, sin lifetime, sin cifrado |
| **Spec Kitty** | specs repo-native + worktrees | cleartext, solo specs |
| **Paper control-plane** ([arxiv jun-2026](https://arxiv.org/html/2606.26924v1)) | tiers de permiso determinísticos, fail-closed | gatea _tools_, no _docs_; sin storage/lifecycle |
| **SOPS / age** ([getsops.io](https://getsops.io/)) | cifrado sobre git | sin lifecycle, sin discovery, sin inyección |
| **AGENTS.md / CLAUDE.md** | discovery + auto-inyección | solo evergreen; gitignore como único control de acceso |

**Ningún tool combina visibilidad × duración × worktree-native × cifrado × harness-agnostic.** Ese
crossproduct es el hueco de `doctier`, confirmado por ausencia de contraevidencia en 22 fuentes.

## 4. Diferenciación defendible

Tres capacidades que salen del cruce de ejes y que el reporte marca como huecos abiertos del
ecosistema:

1. **Decrypt-into-context (el filtro smudge).** Los agentes necesitan _plaintext en el momento de
   lectura_, pero el repo debe guardar ciphertext. `doctier` descifra en checkout: el agente lee
   claro localmente, git guarda cifrado. El reporte señala este patrón como una **pregunta abierta**
   del ecosistema; `doctier` ya lo resuelve.
2. **Worktree-native por tracked-encrypted.** Como el doc privado va _rastreado_ (cifrado), viaja
   por git nativo a cada worktree **sin `.worktreeinclude` ni hooks de sembrado**. El reporte
   confirma que lo gitignored debe duplicarse per-worktree; `doctier` lo evita.
3. **Efímero + GC determinístico.** Automatiza el descarte que hoy es manual, con disparadores
   `pr-merge` / `worktree` / `ttl` y `check` fail-closed.

## 5. Implicaciones de roadmap

Prioridades derivadas del análisis (orden = valor estratégico):

- **P0 — Blindar el cruce (ya en prototipo).** El diferenciador es la _combinación_ de ejes, no
  cada eje por separado. Endurecer manifiesto, filtros clean/smudge, `check` y `gc` hasta que el
  cruce sea sólido end-to-end antes de ampliar superficie.
- **P1 — Puente a AGENTS.md (movimiento de mayor valor).** La descubribilidad automática es la única
  de las necesidades del hilo que `doctier` **no** cubre, y AGENTS.md ya la posee. **No competir:
  integrar.** `doctier` debe emitir punteros _tier-aware_ hacia AGENTS.md/CLAUDE.md (p. ej. generar
  un bloque que liste los docs visibles para el agente según el estado de trabajo). Esto cierra el
  hueco y convierte a AGENTS.md de competidor en canal de distribución.
- **Recetas de `gc` por host (CI)** para el disparador `pr-merge`, que es intrínsecamente
  dependiente del host (ver [`DESIGN.md`](DESIGN.md) §7.1, §14).

**No-objetivos que mantener** (el reporte confirma que sostienen el nicho defendible):

- No ser gestor documental, wiki ni herramienta de **colaboración/comentarios**. Nadie serio lo
  resuelve por esta vía; añadirlo diluye el foco.
- No competir con herramientas spec-driven (Spec Kit, Kiro, Tessl) ni de memoria (Letta): `doctier`
  es la **capa transversal** que ninguna tiene, no un sustituto de ninguna.

## 6. Riesgo principal y ventana

**Amenaza:** si AGENTS.md (ahora bajo Linux Foundation) añade una extensión de _lifecycle_ o de
_declaración de permisos_, absorbe los dos ejes de `doctier` desde el lado de la convención, no del
tooling. La ventana consiste en **cerrar y hacer conocido el cruce antes de que eso pase**, y en que
el puente a AGENTS.md (P1) posicione a `doctier` como el implementador natural de esos ejes si la
convención los estandariza.

**Time-sensitivity:** el espacio se mueve en meses (paper jun-2026, `.worktreeinclude` jul-2026,
AGENTS.md → Linux Foundation). Este mapa es una foto; re-verificar competidores y convenciones cada
pocos meses.

---

*Basado en una investigación multi-fuente con verificación adversarial (20 afirmaciones confirmadas
3-0, 5 refutadas), realizada el 1 de julio de 2026. Fuentes primarias citadas en línea.*
