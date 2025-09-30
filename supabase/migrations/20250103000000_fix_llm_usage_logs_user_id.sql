-- Fix user_id column in llm_usage_logs table to be a proper foreign key
-- This migration corrects the user_id column to match the pattern used in other tables

-- First, drop the existing index that uses the old column
DROP INDEX IF EXISTS idx_llm_usage_logs_user_date;

-- Drop the existing RLS policy that references the old column
DROP POLICY IF EXISTS "Users can view their own usage logs" ON llm_usage_logs;

-- Alter the user_id column to be a proper UUID foreign key
ALTER TABLE llm_usage_logs 
  ALTER COLUMN user_id TYPE UUID USING user_id::UUID,
  ADD CONSTRAINT fk_llm_usage_logs_user_id 
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON DELETE CASCADE;

-- Recreate the index with the correct column type
CREATE INDEX IF NOT EXISTS idx_llm_usage_logs_user_date ON llm_usage_logs(user_id, created_at);

-- Recreate the RLS policy with the correct column type
CREATE POLICY "Users can view their own usage logs" ON llm_usage_logs
  FOR SELECT USING (auth.uid() = user_id);
