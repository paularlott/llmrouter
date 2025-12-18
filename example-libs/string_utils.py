def reverse_string(s):
    """Return the reverse of a string"""
    return s[::-1]

def to_uppercase(s):
    """Convert a string to uppercase"""
    return s.upper()

def to_lowercase(s):
    """Convert a string to lowercase"""
    return s.lower()

def capitalize_words(s):
    """Capitalize the first letter of each word"""
    return ' '.join(word.capitalize() for word in s.split())

def count_words(s):
    """Count the number of words in a string"""
    return len(s.split())

def remove_spaces(s):
    """Remove all spaces from a string"""
    return s.replace(' ', '')

def is_palindrome(s):
    """Check if a string is a palindrome (ignoring case and spaces)"""
    cleaned = s.lower().replace(' ', '')
    return cleaned == cleaned[::-1]