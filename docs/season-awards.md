# Rating base y premios de temporada — fórmulas v3

Estado: **activo** · catálogo `go-football/season-awards` v3.

Fuentes versionadas:

- algoritmo ejecutable: [`internal/performance/algorithm.go`](../internal/performance/algorithm.go);
- catálogo de premios: [`internal/awards/formulas/season-v3.json`](../internal/awards/formulas/season-v3.json);
- publicación SQL: [`007_provider_independent_ratings.sql`](../data/db/migrations/007_provider_independent_ratings.sql).

La v3 retira la v2 sin modificarla. **Ninguna fórmula v3 usa el rating de
SofaScore ni otro rating publicado por un proveedor.** La columna histórica se
puede conservar para trazabilidad, pero no se consulta al calcular rankings.

## 1. Datos y normalización

Solo cuentan partidos finalizados y jugadores con posición resuelta. Conteos
como goles, entradas o intercepciones se agregan y se expresan por 90 minutos.
Porcentajes como precisión de pase, duelos, regates, balones largos, atajadas y
salidas se contraen primero hacia la tasa de su posición:

```text
rate* = (éxitos + intentos_previos × tasa_posición)
        / (intentos + intentos_previos)
```

Esto impide que un 100% en dos intentos supere automáticamente una tasa estable
en cuarenta. Después cada métrica se convierte en un Z-score robusto dentro de
la misma edición, posición y ventana:

```text
Zr(x) = (x − mediana_posición) / (1,4826 × MAD_posición)
```

Si el MAD es cero se usa el Z-score poblacional; si tampoco hay dispersión, el
resultado es cero. Aplicar un filtro de posición en la web no cambia la cohorte.

## 2. Rating base por posición

La calidad `Q` combina cinco dimensiones normalizadas:

| Posición | Ataque | Progresión | Defensa | Control | Influencia |
| --- | ---: | ---: | ---: | ---: | ---: |
| GK | 5% | 10% | 10% | 30% | 45% |
| DEF | 10% | 20% | 35% | 20% | 15% |
| MID | 15% | 30% | 15% | 20% | 20% |
| FWD | 35% | 20% | 5% | 15% | 25% |

Las dimensiones usan acciones observables:

```text
Ataque = per90(5×goles + 3×asistencias + 0,5×remates al arco)
Progresión = per90(progresión + conducciones progresivas + 2×pases clave)
Defensa = per90(1,5×entradas + 1,5×intercepciones + 0,75×bloqueos
          + 0,5×recuperaciones + 0,5×despejes + 0,2×duelos ganados − errores)
Control = 0,50×%pase* + 0,25×%duelo* + 0,25×%regate*
          − 0,25×pérdidas/toques
Influencia = per90(toques + 4×pases clave + 2×remates + 2×faltas recibidas)
```

## 3. Ajuste de minutos y escala pública

```text
R = Min / (Min + 900)
B = Q × R
V = √(Min / Minmáx)
Rating general = 0,85×B + 0,15×V
```

`R` vale 9% a 90 minutos, 33% a 450, 50% a 900 y 67% a 1.800. Así una muestra
corta conserva su señal, pero no recibe la misma confianza que una temporada
sostenida. El score estandarizado sin acotar decide el orden. La interfaz aplica
`100/(1+exp(−score))` para mostrar 0–100; al ser monótona, no cambia el ranking.

## 4. Fórmulas de premios

Todos los premios parten de `B` —o de su equivalente en la ventana relevante— y
añaden métricas propias de la categoría.

### Jugador de la temporada

```text
JTS = 0,85×B + 0,15×V
```

### Defensa de la temporada

Solo posición `DEF`. El bloque especialista normaliza duelos, juego aéreo,
intercepciones, entradas, bloqueos, recuperaciones, progresión y errores.

```text
DTS = 0,35×B + 0,50×(DEF especialista × R) + 0,15×V_DEF
```

### Portero de la temporada

`SOTFaced = atajadas + goles recibidos`. La precisión de atajadas se ajusta por
intentos antes de normalizarse.

```text
PTS = 0,25×B
    + 0,35×ShotStopping fiable
    + 0,15×Distribución
    + 0,075×Control de centros
    + 0,075×Salidas
    + 0,05×Errores evitados
    + 0,05×V_GK
```

### Jugador joven de la temporada (Sub-23)

Usa la fórmula general sin bono de edad. Exige fecha de nacimiento y edad menor
a 23 años en `edition.starts_on`.

```text
JJTS = 0,85×B + 0,15×V
```

### Suplente más decisivo

Solo usa minutos y acciones posteriores a la entrada. Un gol vale `1,0`, una
asistencia `0,7`, y ambos se multiplican por el leverage del marcador.

```text
SMS = 0,55×Impacto fiable + 0,25×B_sub + 0,20×V_sub
```

### Jugador con mayor progresión

Compara la primera mitad de jornadas con la segunda mitad de la edición; exige
minutos en ambas ventanas.

```text
MPS = 0,45×(B_new − B_old)
    + 0,20×ΔEficiencia
    + 0,20×ΔInfluencia
    + 0,15×Z_posición(ln((Min_new+90)/(Min_old+90)))
```

## 5. Integridad y publicación

- Un dato realmente ausente no se interpreta como cero; solo se usa cero cuando
  la semántica verificada de la fuente significa que no hubo acciones.
- La proyección web se recalcula con partidos finalizados y no crea un ganador
  oficial.
- Una selección oficial publicada prevalece sobre la proyección.
- Corregir datos crea otra versión de selección. Cambiar pesos o reglas exige una
  nueva versión de fórmula.
