import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { fetchMyProfile, addUserSkill } from "@/features/auth/api";
import { fetchSkills } from "@/features/skills/api";
import { useAuthStore } from "@/lib/auth-store";
import { GraphQLError } from "@/lib/graphql-client";

const addSkillSchema = z.object({
  skillId: z.string().min(1, "Pick a skill"),
  proficiency: z.string().min(1, "Required"),
});

type AddSkillValues = z.infer<typeof addSkillSchema>;

export function ProfilePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { accessToken, userId, clearAuth } = useAuthStore();

  const { data: profile, isLoading, isError } = useQuery({
    queryKey: ["myProfile", userId],
    queryFn: () => fetchMyProfile(accessToken!),
    enabled: !!accessToken,
  });

  const { data: skills } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });

  const form = useForm<AddSkillValues>({
    resolver: zodResolver(addSkillSchema),
    defaultValues: { skillId: "", proficiency: "" },
  });

  const addSkillMutation = useMutation({
    mutationFn: ({ skillId, proficiency }: AddSkillValues) =>
      addUserSkill(accessToken!, skillId, proficiency),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myProfile", userId] });
      form.reset();
    },
  });

  function handleLogout() {
    clearAuth();
    navigate("/login", { replace: true });
  }

  return (
    <div className="mx-auto grid max-w-md gap-6 p-8">
      <Card>
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

      <Card>
        <CardHeader>
          <CardTitle>Add a skill to your profile</CardTitle>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form
              className="grid gap-4"
              onSubmit={form.handleSubmit((values) => addSkillMutation.mutate(values))}
            >
              <FormField
                control={form.control}
                name="skillId"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Skill</FormLabel>
                    <FormControl>
                      <select
                        className="border-input h-8 rounded-md border bg-transparent px-2 text-sm"
                        {...field}
                      >
                        <option value="">Select a skill...</option>
                        {skills?.map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name}
                          </option>
                        ))}
                      </select>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="proficiency"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Proficiency</FormLabel>
                    <FormControl>
                      <Input placeholder="e.g. intermediate" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {addSkillMutation.isError && (
                <p className="text-destructive text-sm">
                  {addSkillMutation.error instanceof GraphQLError
                    ? addSkillMutation.error.message
                    : "Something went wrong. Try again."}
                </p>
              )}
              <Button type="submit" disabled={addSkillMutation.isPending}>
                {addSkillMutation.isPending ? "Adding..." : "Add skill"}
              </Button>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
