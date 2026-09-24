-- sliding_window.lua
--
-- Atomic sliding-window rate limit check-and-increment, for FluxGate.
--
-- KEYS[1] = the ZSET key for this rate-limit identity (e.g. "ratelimit:{api_key}")
-- ARGV[1] = now_ms        (current time in ms, supplied by caller for determinism)
-- ARGV[2] = window_ms     (sliding window size in ms)
-- ARGV[3] = limit         (max requests allowed within the window)
-- ARGV[4] = member_suffix (entropy string, guarantees unique ZSET member)
-- ARGV[5] = ttl_ms        (TTL to apply to the key, e.g. window_ms * 2)
--
-- Returns a 3-element array:
--   { allowed (1 or 0), current_count, oldest_score_ms }

local key        = KEYS[1]
local now_ms     = tonumber(ARGV[1])
local window_ms  = tonumber(ARGV[2])
local limit      = tonumber(ARGV[3])
local member_suf = ARGV[4]
local ttl_ms     = tonumber(ARGV[5])

-- Step 1: Compute window start
local window_start = now_ms - window_ms

-- Step 2: Evict all expired members
redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)

-- Step 3: Count requests remaining within the window
local current_count = redis.call('ZCARD', key)

-- Step 4: Check limit and conditionally add
local allowed = 0
if current_count < limit then
    allowed = 1
    local member = tostring(now_ms) .. '-' .. member_suf
    redis.call('ZADD', key, now_ms, member)
    current_count = current_count + 1
else
    -- Rejected requests do not consume capacity (D13)
end

-- Step 6: Refresh TTL defensively
redis.call('PEXPIRE', key, ttl_ms)

-- Step 7: Get oldest score for X-RateLimit-Reset calculation
local oldest_score_ms = -1
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
if #oldest == 2 then
    oldest_score_ms = tonumber(oldest[2])
end

return { allowed, current_count, oldest_score_ms }
