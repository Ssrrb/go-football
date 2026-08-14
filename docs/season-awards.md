# Premios de temporada — fórmulas v2

Estado: **activo** · catálogo `go-football/season-awards` v2.

La definición versionada está en
[`internal/awards/formulas/season-v2.json`](../internal/awards/formulas/season-v2.json).
La versión 2 se publica mediante
[`006_season_awards_v2.sql`](../data/db/migrations/006_season_awards_v2.sql),
sin modificar las fórmulas v1 ya publicadas.

## Regla común

Solo cuentan partidos finalizados. Cada estadística de rendimiento se convierte
primero en un **Z-score robusto dentro de la posición**:

```text
Zr(x) = (x − mediana_posición) / (1,4826 × MAD_posición)
```

Si el MAD es cero, se usa el Z-score convencional de la posición; si tampoco hay
dispersión, el valor es cero. Antes de estandarizar, los porcentajes se contraen
hacia la media de su posición con una muestra previa específica de intentos. Esto
evita que, por ejemplo, un 100% en dos duelos se trate igual que un 100% en
cuarenta. Entradas, intercepciones y demás conteos se expresan por 90 minutos.

La calidad posicional es:

```text
Q = wA×Ataque + wP×Progresión + wD×Defensa + wC×Control + wI×Influencia
R = Min / (Min + 900)
V = √(Min / Minmáx)
```

| Posición | Ataque | Progresión | Defensa | Control | Influencia |
| --- | ---: | ---: | ---: | ---: | ---: |
| GK | 5% | 10% | 10% | 30% | 45% |
| DEF | 10% | 20% | 35% | 20% | 15% |
| MID | 15% | 30% | 15% | 20% | 20% |
| FWD | 35% | 20% | 5% | 15% | 25% |

En la jornada 4 de 22 no se imponen mínimos propios de una temporada terminada:
la fiabilidad `R` reduce automáticamente las muestras cortas. La puntuación
estandarizada sin acotar decide el orden. Para mostrarla en la interfaz se aplica
`100/(1+exp(−score))`; esta transformación monótona no cambia el ranking.

## 1. Jugador de la temporada

```text
JTS = 0,80 × (Q × R) + 0,20 × V
```

## 2. Defensa de la temporada

Solo participan jugadores con posición `DEF`.

```text
DEF = 0,20×Z(% duelos ganados ajustado)
    + 0,15×Z(% duelos aéreos ganados ajustado)
    + 0,20×Z(intercepciones/90)
    + 0,15×Z(entradas ganadas/90)
    + 0,10×Z(bloqueos/90)
    + 0,10×Z(recuperaciones/90)
    + 0,10×Z(progresión/90)
    − 0,10×Z(errores/90)

DTS = 0,85 × (DEF × R) + 0,15 × V
```

## 3. Portero de la temporada

La fuente actual no publica PSxG, por lo que se usa el porcentaje de atajadas,
ajustado por intentos y estandarizado. `SOTFaced = atajadas + goles recibidos`.

```text
R_SOT = SOTFaced / (SOTFaced + 100)
ShotStop* = Z(% atajadas ajustado) × R_SOT

PTS = 0,40×ShotStop*
    + 0,20×Distribution
    + 0,15×CrossControl
    + 0,10×Sweeping
    + 0,10×ErrorAvoidance
    + 0,05×V
```

Distribución combina precisión de pases y balones largos; control de centros usa
reclamos altos y despejes de puño; sweeping usa salidas exitosas/intentos; evitar
errores invierte errores que terminan en remate o gol. Todos se estandarizan entre
porteros. Si se incorpora PSxG, `ShotStop` pasa a
`Z((PSxG − GA) / SOTFaced)` sin cambiar los pesos.

## 4. Jugador joven de la temporada (Sub-23)

Usa exactamente `JTS`, sin bonificación por edad. Solo admite jugadores con
**menos de 23 años en `edition.starts_on`** y fecha de nacimiento conocida.

```text
JJTS = 0,80 × (Q × R) + 0,20 × V, con edad < 23
```

## 5. Suplente más decisivo

Solo se usan minutos y acciones posteriores a la entrada desde el banco.

```text
R_sub = MinSub / (MinSub + 450)
V_sub = √(SubApps / SubApps_máx)
Impact = Σ(ActionValue × Leverage)
Impact* = Z_posición(Impact) × R_sub
Q_sub* = Q_sub × R_sub
SMS = 0,55×Impact* + 0,25×Q_sub* + 0,20×V_sub
```

Un gol vale `1,0` y una asistencia `0,7`. El estado es el inmediatamente anterior
a la acción:

| Estado al producirse la acción | Leverage |
| --- | ---: |
| Perdiendo por 1 | 1,30 |
| Empatando | 1,20 |
| Ganando por 1 | 1,00 |
| Perdiendo por 2 o más | 0,90 |
| Ganando por 2 | 0,70 |
| Ganando por 3 o más | 0,50 |

## 6. Jugador con mayor progresión

La comparación es obligatoriamente **primera mitad contra segunda mitad**. En una
temporada de 22 jornadas:

- período anterior: jornadas 1–11;
- período nuevo: jornadas 12–22.

No se divide en dos la muestra disponible. Por ello, en la jornada 4 este premio
aún no muestra candidatos: la segunda mitad no existe. Empieza a calcularse
cuando un jugador registra minutos en ambas mitades.

```text
Q_old* = Q_old × Min_old/(Min_old+900)
Q_new* = Q_new × Min_new/(Min_new+900)
ΔQ = Q_new* − Q_old*

Efficiency = Z(SuccessScore) − Z(FailureScore)
ΔEfficiency = Efficiency_new − Efficiency_old
ΔInfluence = Influence_new − Influence_old
RoleGrowth = ln((Min_new+90)/(Min_old+90))

MPS = 0,45×ΔQ
    + 0,20×ΔEfficiency
    + 0,20×ΔInfluence
    + 0,15×Z_posición(RoleGrowth)
```

## Integridad y publicación

- Un dato realmente ausente no se interpreta como cero. Solo se trata como cero
  un conteo omitido cuando la semántica comprobada del proveedor significa que
  no hubo acciones.
- La proyección de la web se recalcula desde partidos finalizados y no crea un
  ganador oficial.
- Una selección oficial publicada tiene precedencia sobre la proyección.
- Las versiones publicadas son inmutables: v1 se retira y v2 se inserta como una
  definición nueva; las correcciones de datos generan otra versión de selección,
  no otra fórmula.
