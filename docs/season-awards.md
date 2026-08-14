# Premios de temporada — fórmulas v1

Estado: **activo** · catálogo `go-football/season-awards` v1.

La definición ejecutable está en
[`internal/awards/formulas/season-v1.json`](../internal/awards/formulas/season-v1.json)
y se publica en PostgreSQL mediante
[`005_season_awards.sql`](../data/db/migrations/005_season_awards.sql). Este
documento explica las decisiones deportivas; el JSON es la referencia exacta
de pesos, elegibilidad y desempates.

## Escala, normalización y publicación

Cada métrica se calcula únicamente con partidos finalizados de la misma edición.
Los valores extremos se limitan a los percentiles 5 y 95 y después se convierten
en rango percentil `[0, 1]` dentro de los jugadores elegibles para el premio. En
métricas negativas (errores o disciplina), el percentil se invierte. Los empates
de una métrica reciben el rango promedio.

La puntuación normal es:

```text
puntos = 100 × Σ(pesoᵢ × percentilᵢ)
```

Se redondea una vez a milipuntos (`72.431 = 72431`). No se convierte un dato
ausente en cero. Una métrica obligatoria ausente deja al jugador fuera; un peso
opcional solo se redistribuye cuando la fórmula lo autoriza. No se publica un
premio con menos de diez candidatos elegibles. Una edición debe estar finalizada
para publicar ganadores; durante el torneo solo pueden mostrarse proyecciones.

Una fórmula activa no se edita. Un cambio de pesos, edad, ventanas o métricas:

1. retira la versión activa;
2. inserta una nueva fila en `season_award_formula` con `version + 1`;
3. recalcula todos los candidatos contra un hash de entrada nuevo;
4. publica una nueva `season_award_selection` y marca la anterior como
   `superseded`.

De este modo una corrección del proveedor cambia la **versión de selección**, no
la fórmula. El `breakdown` de cada candidato conserva métricas, percentiles,
pesos, minutos y desempates aplicados.

## Métricas compartidas

- **Disponibilidad:** minutos / minutos reglamentarios posibles de su equipo
  mientras estuvo registrado.
- **Consistencia:** 50% de la proporción de ratings ≥ 7,0 y 50% de la inversa de
  la desviación absoluta mediana de sus ratings.
- **Producción:** índice por 90 calculado con datos canónicos: `5×goles +
  3×asistencias + 0,5×remates al arco + 0,10×duelos ganados + 0,15×entradas +
  0,10×intercepciones + 0,05×despejes + 0,05×recuperaciones + 0,01×pases`, menos
  `0,20×pérdidas + 0,50×faltas + amarillas + 3×rojas`. Primero se percentila por
  posición para no favorecer un rol por volumen estadístico.
- **Influencia para el equipo:** 40% de su parte de la producción positiva del
  equipo, 30% de su parte de goles + asistencias y 30% de los puntos ligueros del
  equipo ponderados por sus minutos. Es una medida de participación, no afirma
  causalidad.
- **Índice de acierto:** promedio ponderado de precisión en pases (35%), entradas
  (20%), regates (15%), centros (10%), balones largos (10%) y remates al arco
  (10%). Cada porcentaje usa `completados / intentados`. Los mínimos de intentos
  por ventana son 50, 5, 5, 5, 10 y 5 respectivamente; los pesos disponibles se
  redistribuyen y deben existir al menos cuatro categorías.
- **Índice de falla:** 60% de acciones técnicas falladas, 25% de pérdidas por
  toque y 15% de faltas/tarjetas por 90. Una amarilla pesa 2 y una roja 5.

## 1. Jugador de la temporada

Elegibilidad: posición resuelta, al menos `max(900 minutos, 50% de los minutos
posibles)`, diez apariciones y rating publicado en 70% de las apariciones.

| Componente | Peso |
| --- | ---: |
| Rating medio ponderado por minutos | 35% |
| Índice de producción por 90 | 25% |
| Influencia para el equipo | 20% |
| Consistencia | 10% |
| Disponibilidad | 10% |

Desempates: minutos, rating medio, apariciones y UUID estable.

## 2. Defensa de la temporada

Solo posición resuelta `DEF`. Elegibilidad: al menos `max(900 minutos, 50% de
los posibles)`, diez apariciones y 60% de cobertura de rating.

| Componente | Peso |
| --- | ---: |
| Producción defensiva por 90 (entradas, intercepciones, despejes y recuperaciones) | 30% |
| Eficacia en entradas y duelos | 20% |
| Porterías a cero y pocos goles recibidos mientras juega | 15% |
| Rating medio | 15% |
| Influencia dentro de defensas | 10% |
| Disciplina, invertida | 10% |

Desempates: menos goles recibidos por 90, minutos, menos rojas y UUID.

## 3. Portero de la temporada

Solo `GK`. Elegibilidad: al menos `max(720 minutos, 40% de los posibles)`, ocho
apariciones y 25 remates al arco enfrentados.

| Componente | Peso |
| --- | ---: |
| Goles evitados (PSxG − goles); respaldo estadístico si no hay PSxG | 30% |
| Porcentaje de atajadas | 25% |
| Control del área | 15% |
| Porterías a cero ponderadas por minutos | 15% |
| Distribución con pases y balones largos | 10% |
| Errores y disciplina, invertidos | 5% |

El respaldo para goles evitados compara las atajadas reales con las esperadas
dado el volumen de remates al arco del mismo torneo; nunca inventa un PSxG.
Desempates: goles evitados, porcentaje de atajadas, minutos y UUID.

## 4. Jugador joven de la temporada (Sub-23)

“Sub-23” significa tener **menos de 23 años en `edition.starts_on`**; se requiere
fecha de nacimiento y posición resuelta. Elegibilidad: al menos `max(540
minutos, 30% de los posibles)`, ocho apariciones y 60% de cobertura de rating. Usa los mismos pesos
del Jugador de la temporada para no premiar solo potencial o juventud.

Desempates: menor edad al inicio, minutos, rating medio y UUID.

## 5. Suplente más decisivo de la temporada

Cuenta solo apariciones y minutos iniciados como `substitute`. Se exigen cinco
entradas, 180 minutos como suplente y no haber sido titular en más de la mitad de
sus apariciones.

| Componente | Peso |
| --- | ---: |
| Acciones decisivas por 90: gol ganador 4, empate 3, otro gol 2, asistencia 1,5 | 35% |
| Cambio de puntos ligueros entre su entrada y el final | 25% |
| Índice de producción por 90 como suplente | 20% |
| Frecuencia de apariciones con gol, asistencia o cambio positivo | 10% |
| Fiabilidad por muestra (`min(1, minutos/540)`) | 10% |

El estado del marcador en `match_event` determina si un gol empata o gana; no se
infiere solo del resultado final. Desempates: goles ganadores + empatadores,
asistencias, minutos como suplente y UUID.

## 6. Jugador con mayor progresión

Este premio compara dos ventanas estables. Con `N` jornadas finalizadas y
`k = ceil(0,35 × N)`, toma las primeras `k` y las últimas `k`, ordenadas por
número; las jornadas centrales se excluyen. Se requieren al menos diez jornadas
y ventanas sin solapamiento. También se exigen 270 minutos, cuatro apariciones y
60% de cobertura de rating **en cada ventana**,
además de datos para cuatro de las seis categorías de acierto.

| Crecimiento (ventana final − inicial) | Peso |
| --- | ---: |
| Calidad: rating ponderado por minutos | 30% |
| Porcentaje de aciertos | 20% |
| Reducción del porcentaje de fallas | 20% |
| Influencia para su equipo | 20% |
| Cuota de minutos/confianza del entrenador | 10% |

Aquí se centra el resultado en 50 para distinguir mejora y retroceso:

```text
fiabilidad = min(1, √(min(minutos_iniciales, minutos_finales) / 540))
score = clamp(50 + 50 × fiabilidad × Σ[pesoᵢ × (2 × percentil(Δᵢ) − 1)], 0, 100)
```

Así, los minutos afectan tanto la elegibilidad como la confianza del cálculo;
un pico de pocos minutos no supera automáticamente una progresión sostenida.
Los aciertos proceden siempre de completados/intentos. Las fallas incluyen
intentos no completados, pérdidas, faltas y tarjetas. La influencia compara la
participación del jugador en el rendimiento y las contribuciones de su equipo
en ambas ventanas. Desempates: crecimiento de influencia, crecimiento de
acierto, minutos de la ventana final y UUID.

## Límites de interpretación

- Los percentiles comparan al jugador con su cohorte y edición, no entre ligas.
- “Influencia” es descriptiva y no sustituye un modelo causal de on/off.
- Las transferencias se agregan por jugador; el denominador de equipo se pondera
  por los minutos registrados con cada club.
- Partidos abandonados, anulados o sin estadísticas completas no entran en una
  publicación hasta que la política de finalización los resuelva.
