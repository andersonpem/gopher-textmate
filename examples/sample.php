<?php

namespace App\Service;

use App\Model\User;

/**
 * Greeter builds greeting messages.
 */
class Greeter
{
    private string $prefix;

    public function __construct(string $prefix = "Hello")
    {
        $this->prefix = $prefix;
    }

    public function greet(User $user): string
    {
        $count = 42;
        return "{$this->prefix}, {$user->name}! You have {$count} messages.";
    }
}
