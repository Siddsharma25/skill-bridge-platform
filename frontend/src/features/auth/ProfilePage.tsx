import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchMyProfile } from "@/features/auth/api";
import { useAuthStore } from "@/lib/auth-store";

export function ProfilePage() {
  const navigate = useNavigate();
  const { accessToken, userId, clearAuth } = useAuthStore();

  const { data: profile, isLoading, isError } = useQuery({
    queryKey: ["myProfile", userId],
    queryFn: () => fetchMyProfile(accessToken!),
    enabled: !!accessToken,
  });

  function handleLogout() {
    clearAuth();
    navigate("/login", { replace: true });
  }

  return (
    <div className="flex min-h-svh items-center justify-center p-8">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>My profile</CardTitle>
          <CardDescription>userId: {userId}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          {isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
          {isError && (
            <p className="text-destructive text-sm">
              Couldn't load your profile. Your session may have expired.
            </p>
          )}
          {profile && (
            <>
              <div>
                <p className="text-sm font-medium">Display name</p>
                <p className="text-muted-foreground text-sm">
                  {profile.displayName || "(not set yet)"}
                </p>
              </div>
              <div>
                <p className="text-sm font-medium">Bio</p>
                <p className="text-muted-foreground text-sm">{profile.bio || "(not set yet)"}</p>
              </div>
              <div>
                <p className="text-sm font-medium">Skills</p>
                {profile.skills.length === 0 ? (
                  <p className="text-muted-foreground text-sm">No skills added yet.</p>
                ) : (
                  <ul className="text-muted-foreground text-sm list-disc pl-4">
                    {profile.skills.map((s) => (
                      <li key={s.skill.id}>
                        {s.skill.name} — {s.proficiency}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          )}
          <Button variant="outline" onClick={handleLogout}>
            Log out
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
