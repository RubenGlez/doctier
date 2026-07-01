# Diseño: privacidad y ciclo de vida de documentos generados

> Documento de diseño de **`doctier`**: una **CLI standalone, agnóstica de cualquier arnés,
> que vive en su propio repositorio** y funciona sobre git. Clasifica los documentos que un
> flujo de trabajo genera según **dos ejes independientes —visibilidad y duración— definidos
> por el usuario en un archivo de configuración**, y hace cumplir esa clasificación de forma
> automática. Pensada de raíz para funcionar con **git worktrees** (agentes de codificación en
> paralelo). No incluye implementación; su propósito es acordar el enfoque antes de construir.
>
> Las decisiones tomadas hasta ahora están resumidas en §13.

## 1. Problema

Un flujo de trabajo con agentes genera documentación a lo largo del desarrollo: estrategia de
producto, arquitectura, decisiones, informes de QA, PRDs previos a una feature, notas de
prototipo… Hoy, en el arnés que motiva este diseño, **todo se guarda en `.harness/` y está
gitignored en bloque**: ningún documento viaja por git, así que nada se respalda ni se comparte,
y todo es local.

Eso no encaja con lo que se necesita. Distintos documentos tienen distintas necesidades en **dos
dimensiones que son independientes entre sí**:

- **Quién puede leerlos** — algunos son seguros de compartir (ingeniería, ADRs); otros son la
  ventaja competitiva y no deben ser públicos (estrategia de producto).
- **Cuánto deben vivir** — algunos son permanentes; otros son transitorios (un PRD que se crea
  antes de una feature, se usa durante el desarrollo y la verificación, y debe desaparecer).

### Causa raíz

Git modela **un solo eje y con solo dos estados**: un fichero está rastreado (visible para
quien tenga el repo) o ignorado (local). No existe de forma nativa "privado pero compartido",
ni "se borra solo cuando se cumple una condición". `doctier` construye los **dos ejes que faltan
encima de git**, sin depender de ningún arnés.

## 2. Objetivos y no-objetivos

**Objetivos**
- Modelar la clasificación como **dos ejes independientes**: visibilidad × duración (§3).
- Que **el usuario** decida, en un archivo de configuración, qué patrones caen en cada valor de
  cada eje (§4). La herramienta **no tiene opinión sobre ficheros concretos**.
- Hacer cumplir la clasificación automáticamente, **fail-closed**: que sea imposible por
  accidente publicar contenido privado o dejar sin recolectar un efímero.
- **Agnóstico**: CLI standalone en su propio repo; cualquier proyecto git la adopta. El arnés
  es solo un consumidor más (§11).
- **Agnóstico del host**: igual en GitHub, GitLab o self-hosted, repo público o privado.
- **Worktrees como caso de primera clase** (agentes en paralelo, cada uno en su worktree) (§8).

**No-objetivos**
- No es un gestor documental ni un wiki. Solo clasifica y aplica políticas.
- No sustituye el control de acceso del host para lo público; lo complementa.
- No garantiza ocultar la *existencia* del contenido privado, solo su lectura (ver §6, metadatos).

## 3. Modelo: dos ejes independientes

La pieza conceptual central. Cada documento (o patrón de rutas) se describe con **dos
propiedades ortogonales**:

### Eje A — Visibilidad (quién puede leer el contenido)

- **`public`** — texto plano. Si se rastrea en git, cualquiera con el repo lo lee.
- **`private`** — cifrado (backend `age` por defecto, §6). Solo quien tenga la clave lo lee,
  aunque el blob esté en un repo accesible.

### Eje B — Duración (cuánto vive el fichero)

- **`durable`** — vida indefinida. Persiste hasta que alguien lo cambie o borre a mano.
- **`ephemeral`** — **vida finita**: se **borra automáticamente** cuando se cumple su
  disparador. **Efímero NO significa gitignored** — significa borrado programado. Un efímero
  puede estar perfectamente rastreado (y por tanto viajar por git) durante su vida, y luego
  desaparecer. Disparadores (§7): `pr-merge`, `worktree`, `ttl`.

### La matriz completa

Los dos ejes se combinan libremente; las cuatro celdas son válidas:

| | **Durable** | **Efímero** (vida finita) |
|---|---|---|
| **Público** | Rastreado siempre. *Ej.: arquitectura, ADRs.* | Rastreado; viaja en git; se borra al disparador. *Ej.: PRD que va en la PR y desaparece al fusionar.* |
| **Privado** | Cifrado + rastreado siempre. *Ej.: estrategia de producto.* | Cifrado + rastreado; se borra al disparador. *Ej.: nota estratégica de una feature.* |

**Consecuencia importante para worktrees:** al desacoplar duración de "estar rastreado", tanto
duraderos como efímeros *rastreados* viajan solos a cada worktree por el propio git. El problema
de "sembrar" un worktree casi desaparece (§8).

**Excepción para lo sensible (§7.3):** un efímero marcado como sensible **no se commitea nunca**
(gitignored + local al worktree), para no dejar rastro en la historia de git. Es la única
combinación que sí es local por diseño.

## 4. El archivo de configuración (dirigido por el usuario)

Un único fichero declarativo en la raíz del repo, `.doctier.yml`, versionado. **El usuario
decide todo**: qué rutas son públicas o privadas, cuáles duraderas o efímeras, y —si son
efímeras— con qué disparador y qué parámetros. La herramienta solo lee este archivo.

```yaml
version: 1

# Cada regla: un patrón glob + sus dos ejes. La primera regla que casa, gana.
docs:
  - path: "**/*"                 # regla base: nada queda sin clasificar (fail-closed)
    visibility: public
    lifetime: durable

  - path: "docs/strategy/**"
    visibility: private          # cifrado con age
    lifetime: durable

  - path: "**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire:
      on: pr-merge               # se borra al fusionar la PR

  - path: "docs/strategy/*.wip.md"
    visibility: private
    lifetime: ephemeral
    expire:
      on: ttl
      ttl_days: 30

  - path: "**/_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true              # nunca se commitea: gitignored + local al worktree (§7.3)
    expire:
      on: worktree               # muere con el worktree

# Configuración de los backends/ejes
visibility:
  private:
    backend: age                 # por defecto; enchufable (§6): age | git-crypt | repo-separado
    recipients_file: .doctier/recipients.txt

lifetime:
  ephemeral:
    default_scope: worktree      # para 'on: worktree'; configurable (§7.2)

policy:
  uncovered: block               # doc sin regla que case => se bloquea el commit
```

Punto clave de diseño: **cambiar el backend de privacidad, o reclasificar un fichero, es editar
este archivo**. No cambia cómo el arnés (u otro consumidor) *escribe* los documentos; solo
cambia qué hace `doctier` con ellos.

## 5. Mecanismo derivado de cada combinación

`doctier` traduce cada regla a primitivas de git:

| Visibilidad | Duración | Mecanismo |
|---|---|---|
| public | durable | Rastreado normal. |
| private | durable | Rastreado con filtro `clean/smudge` (age): cifra al `git add`, descifra al checkout. |
| public | ephemeral | Rastreado normal; un disparador (§7) hace `git rm` + commit al expirar. |
| private | ephemeral (no sensible) | Rastreado cifrado; disparador hace `git rm` + commit al expirar (el ciphertext queda en historia). |
| cualquiera | ephemeral + `sensitive: true` | **Nunca rastreado**: gitignored + almacenado local al worktree; se borra del disco al expirar. Sin rastro en historia. |

## 6. Nivel privado: backend (decisión: age por defecto, enchufable)

El contenido privado se persiste y comparte con autorizados pero nunca se lee en claro desde el
repo. El backend es **enchufable** detrás de `visibility.private.backend`, con **`age` por
defecto**. Comparación de las dos opciones viables:

### Opción A — Cifrado in-situ (age) · POR DEFECTO

Mismo repo, filtro `clean/smudge` + `.gitattributes`: el blob guardado es ciphertext; el filtro
`smudge` descifra en cada checkout (y por tanto en cada worktree). Claves gestionadas como
recipients de `age`, **reutilizando las claves SSH** que la gente ya tiene (age las soporta), sin
ceremonia de claves nuevas. `doctier grant/revoke` añade/quita una clave del recipients file y
**re-cifra** los ficheros afectados; revocar = quitar la clave + re-cifrar.

- **Pros**: un solo repo, mínima fricción; agnóstico del host (sirve hasta en repo público);
  historia atómica junto al código; **compatibilidad nativa con worktrees** (viaja por checkout,
  sin hooks de sembrado); onboarding = añadir una clave.
- **Contras**: la historia de git es para siempre → una clave filtrada expone *todas* las
  versiones históricas; los blobs cifrados no diffean/mergean bien (conflictos binarios); **fuga
  de metadatos** (nombres, tamaños, fechas, autores, mensajes de commit siguen públicos);
  gestión/rotación de claves.
- `age` sobre `git-crypt`: claves modernas y multi-recipient más simples; `git-crypt` está atado
  a GPG y con mantenimiento escaso.

### Opción B — Repo privado separado (enchufable, no por defecto)

La estrategia vive en su propio repo git privado, mantenido como **repo hermano sincronizado por
`doctier`** (mejor que submódulo, que sufre con worktrees).

- **Pros**: separación real, cero fuga de contenido y metadatos al repo público; acceso nativo
  del host y **revocable**; texto plano dentro del repo privado (diff/merge/blame normales); sin
  gestión de claves.
- **Contras**: dos repos que sincronizar; **fricción con worktrees** (hay que resolver el repo
  hermano por worktree); cuesta mantener atómico "código + cambio de estrategia".

### Por qué age por defecto

El caso principal es un repo de equipo (privado) donde el objetivo es que *no todo el que tiene
acceso lea la estrategia* y que *no se filtre a forks/mirrors*. Ahí `age` gana: menos fricción,
un repo, y —decisivo aquí— **compatibilidad nativa con worktrees**. La Opción B queda documentada
y enchufable para quien necesite aislamiento duro o acceso revocable (típicamente, repo principal
público). La decisión no queda congelada: se cambia en el manifiesto.

## 7. Duración efímera: disparadores, alcance y borrado

"Efímero" = vida finita. `doctier` soporta tres disparadores de expiración, elegidos por regla en
`expire.on`:

### 7.1 Disparadores

- **`pr-merge`** — se borra cuando la PR/rama se fusiona. Ideal para un PRD que debe existir y
  viajar *dentro* de la PR (revisable), y desaparecer una vez integrada la feature. Detectar el
  merge es intrínsecamente cosa del host (los squash merges no dejan commit de merge; la rama
  puede borrarse en remoto), así que `doctier` se mantiene agnóstico con un **comando genérico
  `doctier gc`** que se invoca desde varios sitios: **CI como primario** (acción en el evento de
  merge; se envían recetas de ejemplo por host), **hook local como refuerzo**, y **TTL como red
  final** por si ambos fallan.
- **`worktree`** — vive mientras exista el worktree; se recolecta al hacer `git worktree remove`.
  Para scratch de un agente concreto.
- **`ttl`** — expira tras `ttl_days` días. Red de seguridad y para material con caducidad natural.

`doctier gc` centraliza la recolección: purga efímeros de ramas/worktrees ya desaparecidos y los
que superan su TTL. Se puede invocar desde hooks, CI o a mano.

### 7.2 Alcance (decisión: configurable, worktree por defecto)

Para `on: worktree`, la unidad de vida es **configurable** (`lifetime.ephemeral.default_scope:
worktree|branch`), con **worktree por defecto**:

- **`worktree`** (defecto): cada worktree tiene sus efímeros aislados; se van con
  `git worktree remove`. Encaja con agentes en paralelo sin colisiones.
- **`branch`**: se asocian al nombre de rama; se recolectan al fusionar/borrar la rama. Útil sin
  worktrees, pero dos worktrees en la misma rama compartirían efímeros.

### 7.3 Borrado y la historia de git (decisión: solo local para lo sensible)

Un efímero **rastreado** que se borra desaparece del árbol de trabajo, pero **su contenido sigue
en la historia de git para siempre**. Para un privado, el ciphertext permanece en la historia
(recuperable, y expuesto si se filtra una clave). Política adoptada, **híbrida según
sensibilidad**:

- **No sensible** → se rastrea y se borra normal (`git rm` + commit). Queda en historia:
  auditable y recuperable. Es el comportamiento estándar y suficiente para la mayoría.
- **`sensitive: true`** → **no se commitea nunca**: gitignored + local al worktree. Así no deja
  rastro en la historia. Al expirar se borra del disco. Es la vía correcta para material efímero
  verdaderamente sensible.

Se descarta la reescritura de historia (`filter-repo`) como mecanismo ordinario: reescribe
historia compartida y es disruptiva. Queda como recurso manual de emergencia, fuera del flujo.

## 8. Compatibilidad con worktrees (agentes en paralelo)

Motivo de ser del diseño: varios agentes trabajando a la vez, cada uno en su `git worktree`.
Comportamiento por combinación al crear un worktree nuevo:

| Tipo de doc | ¿Llega solo al worktree nuevo? | Mecanismo |
|---|---|---|
| Público/Privado **durable** | **Sí**, nativo | Rastreados; el checkout los trae (privado se descifra con `smudge`). |
| Público/Privado **efímero rastreado** | **Sí**, nativo | Igual que un rastreado cualquiera; luego expira por su disparador. |
| Efímero **sensible/local** (`on: worktree`) | **No** (a propósito) | Gitignored → arranca vacío; es lo correcto (es scratch de esa unidad de trabajo). |

**Conclusión operativa:** al mover públicos y privados a ficheros *rastreados* (cifrados si
procede), **desaparece la necesidad de un hook de "seeding"** como el `harness-seed-worktree.sh`
actual, que hoy existe justamente porque `.harness/` es gitignored en bloque. Solo el scratch
local sigue arrancando vacío, que es lo deseado.

**Aislamiento y recolección:** el scratch local vive dentro del directorio del worktree, así que
dos agentes no colisionan y `git worktree remove` se lo lleva. `doctier gc` cubre worktrees
abandonados (`git worktree prune`) y el TTL cubre huérfanos.

**Consistencia de política:** `.doctier.yml` y `.gitattributes` están rastreados → todos los
worktrees comparten la misma política sin sincronización manual. La clave `age` es de la
máquina/usuario, válida para todos sus worktrees.

## 9. La CLI y su distribución (decisión: standalone en repo propio)

`doctier` es una **CLI standalone en su propio repositorio**, escrita en **Go** (binario único
estático: sin dependencias de runtime, arranque instantáneo en los filtros clean/smudge, y
distribución trivial vía brew / descarga directa / `go install`). Cualquier proyecto git la adopta
con `doctier init`. No arrastra copias de scripts por proyecto.

| Comando | Qué hace |
|---|---|
| `doctier init` | Crea `.doctier.yml`, entradas de `.gitattributes`, instala hooks, genera clave `age`. |
| `doctier check` | Verifica que todo doc casa una regla; que ningún privado está en claro; que ningún sensible está staged. Para pre-commit/pre-push y CI. |
| `doctier gc` | Purga efímeros expirados (pr-merge/worktree/ttl). |
| `doctier grant/revoke <id>` | Gestiona acceso privado (recipients de age / permisos del repo separado). |
| `doctier reveal/hide` | Descifra/cifra en local para editar (backend de cifrado). |
| `doctier status` | Muestra la clasificación efectiva de cada doc y su expiración. |

## 10. Redes de seguridad (fail-closed)

El fallo más grave es **publicar contenido privado en claro** por accidente. El hook
`pre-commit`/`pre-push` (`doctier check`) **falla el commit** si:

- Una ruta `private` está staged **sin cifrar**.
- Una ruta efímera `sensitive` está staged (nunca debe commitearse).
- Un doc **no casa ninguna regla** y `policy.uncovered: block` (fuerza clasificación explícita).

El mismo `doctier check` corre en **CI** como última barrera (no depende de que el cliente tenga
los hooks instalados). Este comportamiento fail-closed es lo que hace la solución fiable: la
seguridad no depende de la memoria de nadie.

## 11. Integración con un consumidor (el arnés, como ejemplo)

`doctier` no conoce al arnés. La adopción por un consumidor es solo:

1. Aportar su propio `.doctier.yml` clasificando sus rutas (ejemplo en el Apéndice A).
2. **Sustituir** cualquier gitignore en bloque de sus docs por reglas conscientes de tiers.
3. **Retirar/aligerar** hooks de seeding: los duraderos y efímeros rastreados ya viajan por git;
   solo el scratch local podría necesitar arranque (y por diseño arranca vacío).
4. Opcionalmente invocar `doctier gc` al cerrar una feature (o confiar en los hooks/CI).

Cualquier otro proyecto git la adopta igual, sin nada del arnés.

## 12. Migración desde el estado actual (del arnés)

1. Añadir `.doctier.yml` con las reglas del consumidor (Apéndice A).
2. Sacar del gitignore lo que pase a ser rastreado (público durable, y privado durable/efímero
   cifrado) y empezar a rastrearlo.
3. Configurar `age` para lo privado.
4. Mantener local solo el efímero `sensitive`; cablear `doctier gc` + hooks para la recolección.
5. Sustituir `harness-gitignore.sh` (ignora en bloque) y aligerar `harness-seed-worktree.sh`.
6. Correr `doctier check` en CI.

Migración incremental: se puede empezar solo por lo privado (lo más urgente) y añadir el resto
después.

## 13. Decisiones tomadas

1. **Backend privado**: `age` por defecto, **enchufable** (repo separado como alternativa). §6.
2. **Clasificación dirigida por el usuario**: un `.doctier.yml` donde el usuario decide
   visibilidad, duración, disparador y TTL por patrón. La herramienta no opina sobre ficheros
   concretos. §4.
3. **Modelo de dos ejes independientes**: visibilidad (public/private) × duración
   (durable/ephemeral). §3.
4. **Alcance del efímero**: configurable (`worktree|branch`), **worktree por defecto**. §7.2.
5. **Distribución**: **CLI standalone en su propio repo** (fuera del arnés). §9.
6. **Efímero = vida finita, no gitignored**; borrado normal para lo no sensible, y **solo local
   (nunca commiteado) para lo sensible**, para no dejar rastro en la historia. §7.
7. **Ecosistema**: **Go**, binario único estático (sin runtime, arranque instantáneo en los
   filtros clean/smudge, distribución trivial). §9.
8. **Claves `age`**: **reutilizar las claves SSH** existentes como recipients (age las soporta);
   `doctier grant/revoke` añade/quita una clave y re-cifra los ficheros afectados. §6.
9. **Disparador `pr-merge`**: comando genérico `doctier gc` invocado desde **CI (primario)** en
   el evento de merge, con **refuerzo por hook local** y **TTL como red final**. §7.1.
10. **Manifiesto**: **YAML**, con precedencia **"primera regla que casa"** (orden explícito
    controlado por el usuario). §4.

## 14. Detalles de implementación pendientes (fase de prototipo)

Ninguna decisión de diseño queda abierta; lo que resta es detalle de construcción:

- **Nombre definitivo** de la CLI (`doctier` es nombre de trabajo).
- **Ceremonia de claves `age`**: rotación de la clave de datos al revocar, custodia y flujo de
  altas/bajas concreto sobre el recipients file.
- **Recetas de CI por host** para `doctier gc` en el evento de merge (GitHub Actions, GitLab CI,
  etc.), más el heurístico del hook local de refuerzo.
- **Detalle del hook local de `pr-merge`**: cómo decidir "esta rama ya está fusionada/desaparecida"
  de forma robusta (squash merges, ramas remotas borradas).

---

## Apéndice A — Ejemplo de configuración para un consumidor tipo arnés

> **No forma parte de la herramienta.** Es solo un ejemplo de cómo *un* proyecto (el arnés)
> podría clasificar sus documentos. Cada proyecto escribe el suyo. Los valores concretos
> (p. ej. si `CONTEXT.md` o `qa/report.md` son públicos, privados, duraderos o efímeros) son
> decisión del usuario de ese proyecto, no de `doctier`.

```yaml
version: 1
docs:
  - path: "**/*"                          # base fail-closed
    visibility: public
    lifetime: durable

  # Estrategia de producto → privada y duradera
  - path: ".harness/product/**"
    visibility: private
    lifetime: durable

  # Ingeniería y decisiones → públicas y duraderas (valor por defecto, explícito por claridad)
  - path: ".harness/engineering/**"
    visibility: public
    lifetime: durable
  - path: ".harness/adr/**"
    visibility: public
    lifetime: durable

  # Informe de QA → foto puntual; ejemplo como efímero por TTL (el usuario decide)
  - path: ".harness/qa/report.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: ttl, ttl_days: 90 }

  # PRD previo a una feature → viaja en la PR y muere al fusionar
  - path: ".harness/**/*.prd.md"
    visibility: public
    lifetime: ephemeral
    expire: { on: pr-merge }

  # Notas de prototipo / scratch → sensibles y locales al worktree
  - path: ".harness/**/_prototype-*"
    visibility: private
    lifetime: ephemeral
    sensitive: true
    expire: { on: worktree }

visibility:
  private: { backend: age, recipients_file: .doctier/recipients.txt }
lifetime:
  ephemeral: { default_scope: worktree }
policy:
  uncovered: block
```
