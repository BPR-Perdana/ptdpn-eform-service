-- Add email_verified and email_verified_at to customers table
ALTER TABLE customers
ADD COLUMN email_verified BOOLEAN DEFAULT FALSE AFTER email,
ADD COLUMN email_verified_at DATETIME NULL AFTER email_verified;
