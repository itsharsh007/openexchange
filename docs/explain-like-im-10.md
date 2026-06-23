# OpenExchange, explained like you're 10

A plain-English tour of what this project is, before any of the jargon. If you ever forget what
the whole thing is *for*, start here.

## It's a pretend stock market 🏪

Imagine a giant marketplace where people don't trade toys or candy — they trade tiny **shares** of
companies. A share is like owning one little Lego brick of a big company. People want to **buy**
bricks (they think the price will go up) or **sell** bricks (they want cash instead). This project
builds that whole marketplace — but with **play money**, like Monopoly, so nobody can lose real
money.

Here's the story of what happens when you want to trade:

### 1. You (the website) 🖥️ — the `web` service
You open the app and see prices going up and down, like a scoreboard. You type: *"I want to buy 10
bricks of Apple for $150 each."* You hit the button.

### 2. The doorman (the gateway) 🚪 — the `gateway` service
Your order runs to the front door. A guard checks: *"Are you allowed in? Are you asking too fast?"*
(so nobody floods the place). If you're okay, he carries your order inside. He also runs back to
tell everyone *"a trade just happened!"* so all the scoreboards update at once.

### 3. The matchmaker (the engine) 🤝 — the `engine` service
This is the brain. It keeps two lines of people: everyone who wants to **buy**, sorted by who'll pay
the most, and everyone who wants to **sell**, sorted by who'll sell cheapest. When a buyer's price
meets a seller's price — *match!* — they trade, instantly. This has to be **super fast and never
make a mistake**, like a referee who never blinks. (This was built first and tested the hardest:
10 bricks went in, exactly 10 bricks came out — never 9 or 11.)

### 4. The notebook (the ledger / Postgres) 📒
Every time bricks change hands, we write it in a permanent notebook: *"You: −$1500, +10 bricks.
Seller: +$1500, −10 bricks."* The two sides must **always add up to zero** — bricks and money are
never magically created or destroyed. If the notebook ever doesn't balance, we *know* something
went wrong. This is how we prove the marketplace is honest.

### 5. The lookout (the risk/ML robot) 🤖 — the `risk` service
A little robot watches all the trades and learns. It tries to guess *"will the price go up next?"*,
and it raises a flag if someone's doing something weird or sneaky — like a security camera that's
also a fortune-teller.

## Why build this giant thing?

Because it secretly contains **every hard computer-science topic at once** — super-fast sorting,
doing many things at the same time without crashing, lots of programs talking to each other,
storing data safely, robots that learn. So when someone asks *"have you built
something complex?"*, you can walk them through this whole marketplace, brick by brick. 🧱

## The grown-up words for each part (so you can connect the two)

| Kid word | Real name | What it really is |
|---|---|---|
| The scoreboard website | `web` | React + TypeScript dashboard |
| The doorman | `gateway` | Go service: REST + WebSocket + rate limiting + auth |
| The matchmaker / referee | `engine` | Java matching engine (the limit order book) |
| The two lines of buyers/sellers | the order book | price-time priority data structure |
| The permanent notebook | `ledger` in Postgres | double-entry accounting (always sums to zero) |
| The lookout robot | `risk` | Python ML: price prediction + anomaly detection |
| The messenger shouting "a trade happened!" | Kafka | event stream connecting the services |

For the technical version of the same story, see [`../README.md`](../README.md) (the "order
lifecycle") and [`architecture.md`](architecture.md).
