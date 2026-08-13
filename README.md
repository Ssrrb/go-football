## Project Description
The goal of this repo is to create a real-time leaderboard system for ranking and scoring of jugadores de la Primera División Paraguaya Clausura 2026. Using data from APF official site.

## Project Requirements

Create a real-time leaderboard system that ranks players based on their scores in each round of the tournament. The system should meet the following requirements:

User Authentication: Users should be able to register and log in to the system.
Scoring: Calculate rating points for each player within each round.
Leaderboard Updates: Display a global leaderboard showing the top players ranked by rating points, filtered by position.
Real-Time Updates: Update the leaderboard in real-time as new scores are entered.
Team of the Week Report: Generate a team of the week report for the top players for each round.

Use Redis Sorted Sets

Leaderboard Storage: Use Redis sorted sets to store and manage leaderboards.
Real-Time Updates: Utilize Redis sorted sets for efficient real-time updates and queries.
Rank Queries: Use Redis commands to query user ranks and leaderboard positions.

## Data 

The data is provided by APF official site, SofaScore and 365 scores.

## Stack

- Python
- Go
- Redis
- Htmx
- Psql
- Docker

